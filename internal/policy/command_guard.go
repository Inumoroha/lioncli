package policy

import (
	"regexp"
	"strings"
)

type shellCommandRule struct {
	reason string
	check  func(shellCommand) bool
}

type shellCommand struct {
	Text string
	Args []string
}

type commandSegment struct {
	text    string
	prevSep byte
}

type commandDenyRule struct {
	reason  string
	pattern *regexp.Regexp
}

var dangerousExecutables = map[string]string{
	"sudo":                "sudo privilege escalation is not allowed",
	"su":                  "switching users is not allowed",
	"shutdown":            "shutdown, reboot, halt, and poweroff are not allowed",
	"reboot":              "shutdown, reboot, halt, and poweroff are not allowed",
	"halt":                "shutdown, reboot, halt, and poweroff are not allowed",
	"poweroff":            "shutdown, reboot, halt, and poweroff are not allowed",
	"mkfs":                "disk formatting commands are not allowed",
	"mkfs.ext4":           "disk formatting commands are not allowed",
	"mkfs.ntfs":           "disk formatting commands are not allowed",
	"diskpart":            "disk partitioning commands are not allowed",
	"format":              "disk formatting commands are not allowed",
	"bcdedit":             "boot configuration changes are not allowed",
	"reg":                 "registry modification commands are not allowed",
	"regedit":             "registry modification commands are not allowed",
	"set-executionpolicy": "PowerShell execution policy changes are not allowed",
}

var shellCommandRules = []shellCommandRule{
	{
		reason: "encoded shell commands are not allowed",
		check: func(cmd shellCommand) bool {
			if !isShellInterpreter(commandName(cmd)) {
				return false
			}
			for _, arg := range cmd.Args[1:] {
				if strings.EqualFold(arg, "-encodedcommand") || strings.EqualFold(arg, "-enc") {
					return true
				}
			}
			return false
		},
	},
	{
		reason: "recursive deletion of root, home, or drive directories is not allowed",
		check: func(cmd shellCommand) bool {
			name := commandName(cmd)
			if name != "rm" && name != "del" && name != "erase" && name != "rd" && name != "rmdir" && name != "remove-item" {
				return false
			}
			return hasRecursiveDeleteFlags(cmd.Args) && containsProtectedPath(cmd.Args)
		},
	},
	{
		reason: "recursive permission changes on root, home, or drive directories are not allowed",
		check: func(cmd shellCommand) bool {
			name := commandName(cmd)
			return (name == "chmod" || name == "chown" || name == "icacls") && hasRecursiveFlag(cmd.Args) && containsProtectedPath(cmd.Args)
		},
	},
	{
		reason: "writing raw data to block devices is not allowed",
		check: func(cmd shellCommand) bool {
			return commandName(cmd) == "dd" && containsArgPrefix(cmd.Args, "of=/dev/")
		},
	},
	{
		reason: "scanning the filesystem root, home, or drive directories is not allowed",
		check: func(cmd shellCommand) bool {
			name := commandName(cmd)
			return (name == "find" || name == "dir" || name == "ls" || name == "get-childitem") && containsProtectedPath(cmd.Args)
		},
	},
	{
		reason: "direct shell execution of downloaded or piped content is not allowed",
		check: func(cmd shellCommand) bool {
			name := commandName(cmd)
			if !isShellInterpreter(name) {
				return false
			}
			return len(cmd.Args) <= 1 || strings.Contains(cmd.Text, "|")
		},
	},
}

// commandDenyRules 是按"单个命令段"做正则拦截的扩展点:CheckCommand 拆分出每个
// 命令段后逐条匹配。目前为空——已知的危险模式都由结构化的 shellCommandRules
// (解析出可执行名+参数后判断,误报更少)或 globalCommandDenyRules(整条命令)覆盖。
// 需要按子串正则补充新规则时往这里加,无需改动 checkCommandText。
var commandDenyRules = []commandDenyRule{}

// globalCommandDenyRules 对整条原始命令(未分段)做正则匹配,用于跨命令段的模式,
// 如 fork bomb —— 它的 ;|&{} 结构会被分段逻辑拆散,只能在整串上识别。
var globalCommandDenyRules = []commandDenyRule{
	{reason: "fork bombs are not allowed", pattern: regexp.MustCompile(`:\(\)\s*\{\s*:\s*\|\s*:\s*&\s*\}\s*;\s*:`)},
}

func CheckCommand(command string) error {
	normalized := strings.TrimSpace(regexp.MustCompile(`\s+`).ReplaceAllString(command, " "))
	if normalized == "" {
		return nil
	}
	for _, rule := range globalCommandDenyRules {
		if rule.pattern.MatchString(normalized) {
			return newError(rule.reason)
		}
	}
	for _, segment := range splitCommandSegments(command) {
		normalizedSegment := strings.TrimSpace(regexp.MustCompile(`\s+`).ReplaceAllString(segment.text, " "))
		if normalizedSegment == "" {
			continue
		}
		if err := checkCommandText(normalizedSegment); err != nil {
			return err
		}
		if segment.prevSep == '|' && isShellInterpreter(commandName(shellCommand{Args: shellFields(normalizedSegment)})) {
			return newError("piping data directly into a shell is not allowed")
		}
		if err := checkStructuredCommand(normalizedSegment); err != nil {
			return err
		}
	}
	return nil
}

func checkCommandText(command string) error {
	for _, rule := range commandDenyRules {
		if rule.pattern.MatchString(command) {
			return newError(rule.reason)
		}
	}
	return nil
}

func checkStructuredCommand(command string) error {
	args := shellFields(command)
	if len(args) == 0 {
		return nil
	}
	if err := checkStructuredArgs(command, args); err != nil {
		return err
	}
	if nested := nestedShellCommand(args); nested != "" {
		return CheckCommand(nested)
	}
	return nil
}

func checkStructuredArgs(command string, args []string) error {
	cmd := shellCommand{Text: command, Args: args}
	if reason, ok := dangerousExecutables[commandName(cmd)]; ok {
		return newError(reason)
	}
	for _, rule := range shellCommandRules {
		if rule.check(cmd) {
			return newError(rule.reason)
		}
	}
	return nil
}

func CommandSegments(command string) []string {
	parts := splitCommandSegments(command)
	segments := make([]string, 0, len(parts))
	for _, part := range parts {
		segments = append(segments, part.text)
	}
	return segments
}

func splitCommandSegments(command string) []commandSegment {
	var segments []commandSegment
	var b strings.Builder
	inSingle := false
	inDouble := false
	inBacktick := false
	escaped := false
	var nextPrevSep byte

	flush := func() {
		if s := strings.TrimSpace(b.String()); s != "" {
			segments = append(segments, commandSegment{text: s, prevSep: nextPrevSep})
		}
		nextPrevSep = 0
		b.Reset()
	}

	for i := 0; i < len(command); i++ {
		ch := command[i]
		if escaped {
			b.WriteByte(ch)
			escaped = false
			continue
		}
		if ch == '\\' && !inSingle {
			b.WriteByte(ch)
			escaped = true
			continue
		}
		switch ch {
		case '\'':
			if !inDouble && !inBacktick {
				inSingle = !inSingle
			}
			b.WriteByte(ch)
		case '"':
			if !inSingle && !inBacktick {
				inDouble = !inDouble
			}
			b.WriteByte(ch)
		case '`':
			if !inSingle && !inDouble {
				inBacktick = !inBacktick
			}
			b.WriteByte(ch)
		case ';', '|', '&', '\n':
			if inSingle || inDouble || inBacktick {
				b.WriteByte(ch)
				continue
			}
			flush()
			if (ch == '|' || ch == '&') && i+1 < len(command) && command[i+1] == ch {
				i++
				nextPrevSep = 0
			} else {
				nextPrevSep = ch
			}
		case '(', ')':
			if inSingle || inDouble || inBacktick {
				b.WriteByte(ch)
				continue
			}
			flush()
		default:
			b.WriteByte(ch)
		}
	}
	flush()
	return segments
}

func shellFields(command string) []string {
	var fields []string
	var b strings.Builder
	inSingle := false
	inDouble := false
	inBacktick := false
	escaped := false
	flush := func() {
		if b.Len() > 0 {
			fields = append(fields, b.String())
			b.Reset()
		}
	}

	for i := 0; i < len(command); i++ {
		ch := command[i]
		if escaped {
			b.WriteByte(ch)
			escaped = false
			continue
		}
		if ch == '\\' && !inSingle && !inDouble {
			escaped = true
			continue
		}
		switch ch {
		case '\'':
			if !inDouble && !inBacktick {
				inSingle = !inSingle
				continue
			}
		case '"':
			if !inSingle && !inBacktick {
				inDouble = !inDouble
				continue
			}
		case '`':
			if !inSingle && !inDouble {
				inBacktick = !inBacktick
				continue
			}
		case ' ', '\t', '\r', '\n':
			if !inSingle && !inDouble && !inBacktick {
				flush()
				continue
			}
		}
		b.WriteByte(ch)
	}
	flush()
	return fields
}

func commandName(cmd shellCommand) string {
	if len(cmd.Args) == 0 {
		return ""
	}
	name := strings.Trim(strings.ToLower(cmd.Args[0]), `"'`)
	name = strings.TrimSuffix(name, ".exe")
	name = strings.ReplaceAll(name, "\\", "/")
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		name = name[idx+1:]
	}
	return name
}

func hasRecursiveDeleteFlags(args []string) bool {
	for _, arg := range args[1:] {
		lower := strings.ToLower(strings.TrimSpace(arg))
		if lower == "-rf" || lower == "-fr" || lower == "/s" || lower == "-recurse" || lower == "-r" {
			return true
		}
		if strings.HasPrefix(lower, "-") && strings.Contains(lower, "r") && strings.Contains(lower, "f") {
			return true
		}
		if strings.HasPrefix(lower, "/") && strings.Contains(lower, "s") {
			return true
		}
	}
	return false
}

func hasRecursiveFlag(args []string) bool {
	for _, arg := range args[1:] {
		lower := strings.ToLower(strings.TrimSpace(arg))
		if lower == "-r" || lower == "-recursive" || lower == "-recurse" || lower == "/s" {
			return true
		}
	}
	return false
}

func containsProtectedPath(args []string) bool {
	for _, arg := range args[1:] {
		if isProtectedPath(arg) {
			return true
		}
	}
	return false
}

func containsArgPrefix(args []string, prefix string) bool {
	for _, arg := range args[1:] {
		if strings.HasPrefix(strings.ToLower(arg), prefix) {
			return true
		}
	}
	return false
}

func isProtectedPath(arg string) bool {
	value := strings.Trim(strings.TrimSpace(arg), `"'`)
	lower := strings.ToLower(value)
	if lower == "" {
		return false
	}
	switch lower {
	case "/", "~", "$home", "${home}", "%userprofile%", "$env:userprofile":
		return true
	}
	if regexp.MustCompile(`^[a-z]:[\\/]?$`).MatchString(lower) || regexp.MustCompile(`^[a-z]:$`).MatchString(lower) {
		return true
	}
	if strings.HasPrefix(lower, "/dev/") || strings.HasPrefix(lower, "/sys/") || strings.HasPrefix(lower, "/proc/") {
		return true
	}
	return false
}

func isShellInterpreter(name string) bool {
	switch name {
	case "sh", "bash", "zsh", "fish", "ksh", "powershell", "pwsh", "cmd":
		return true
	default:
		return false
	}
}

func nestedShellCommand(args []string) string {
	if len(args) < 2 {
		return ""
	}
	name := strings.ToLower(strings.TrimSuffix(args[0], ".exe"))
	if !isShellInterpreter(name) {
		return ""
	}
	for i := 1; i < len(args); i++ {
		lower := strings.ToLower(args[i])
		if lower == "-c" || lower == "/c" || lower == "-command" || lower == "-encodedcommand" {
			if lower == "-encodedcommand" {
				return ""
			}
			return strings.Join(args[i+1:], " ")
		}
	}
	if name == "powershell" || name == "pwsh" {
		return strings.Join(args[1:], " ")
	}
	return ""
}
