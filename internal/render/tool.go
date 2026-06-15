package render

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type ToolCallState string

const (
	ToolStatePending ToolCallState = "pending"
	ToolStateSuccess ToolCallState = "success"
	ToolStateError   ToolCallState = "error"
	ToolStateDenied  ToolCallState = "denied"
)

func ToolLabel(toolName string, count int) string {
	if count <= 0 {
		count = 1
	}
	switch toolName {
	case "read_file":
		return fmt.Sprintf("read %d file(s)", count)
	case "write_file":
		return fmt.Sprintf("write %d file(s)", count)
	case "command_execute":
		return fmt.Sprintf("execute %d command(s)", count)
	case "code_search", "search_code":
		return fmt.Sprintf("search code %d time(s)", count)
	case "web_search":
		return fmt.Sprintf("web search %d time(s)", count)
	case "web_fetch":
		return fmt.Sprintf("fetch %d web page(s)", count)
	case "load_skill":
		return fmt.Sprintf("load %d skill(s)", count)
	default:
		if strings.HasPrefix(toolName, "mcp__") {
			return formatMCPLabel(toolName, count)
		}
		return fmt.Sprintf("%s x %d", toolName, count)
	}
}

func FormatToolArgs(args map[string]any, maxValueChars int) string {
	if len(args) == 0 {
		return ""
	}
	if maxValueChars <= 0 {
		maxValueChars = 400
	}

	keys := make([]string, 0, len(args))
	for key := range args {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		value := formatToolValue(args[key])
		if isSensitiveKey(key) && strings.TrimSpace(value) != "" {
			value = "[redacted]"
		}
		if len([]rune(value)) > maxValueChars {
			value = truncate(value, maxValueChars) + " ...[truncated]"
		}
		lines = append(lines, fmt.Sprintf("%s: %s", key, value))
	}
	return strings.Join(lines, "\n")
}

func KeyParam(toolName string, args map[string]any, maxChars int) string {
	if maxChars <= 0 {
		maxChars = 80
	}
	key := keyParamName(toolName)
	if key == "" {
		return truncate(formatToolValue(args), maxChars)
	}
	value, ok := args[key]
	if !ok {
		return ""
	}
	return truncate(formatToolValue(value), maxChars)
}

func FormatToolCall(toolName string, args map[string]any, state ToolCallState, maxValueChars int) string {
	label := ToolLabel(toolName, 1)
	key := KeyParam(toolName, args, 80)
	if key != "" {
		label += ": " + key
	}
	stateLabel := ToolStateLabel(state)
	if stateLabel == "" {
		return label
	}
	details := FormatToolArgs(args, maxValueChars)
	if details == "" {
		return fmt.Sprintf("[%s] %s", stateLabel, label)
	}
	return fmt.Sprintf("[%s] %s\n%s", stateLabel, label, details)
}

func ToolStateLabel(state ToolCallState) string {
	switch state {
	case ToolStatePending:
		return "pending"
	case ToolStateSuccess:
		return "success"
	case ToolStateError:
		return "error"
	case ToolStateDenied:
		return "denied"
	default:
		return ""
	}
}

func formatMCPLabel(toolName string, count int) string {
	parts := strings.SplitN(toolName, "__", 3)
	display := toolName
	if len(parts) == 3 {
		display = parts[1] + "." + parts[2]
	}
	if count == 1 {
		return "MCP tool " + display
	}
	return fmt.Sprintf("MCP tool %s x %d", display, count)
}

func keyParamName(toolName string) string {
	switch toolName {
	case "read_file", "write_file", "list_dir":
		return "path"
	case "command_execute":
		return "command"
	case "code_search", "search_code", "web_search":
		return "query"
	case "web_fetch":
		return "url"
	case "load_skill":
		return "name"
	default:
		return ""
	}
}

func formatToolValue(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	default:
		raw, err := json.Marshal(v)
		if err == nil {
			return string(raw)
		}
		return fmt.Sprint(v)
	}
}

func isSensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
	for _, marker := range []string{"password", "passwd", "secret", "token", "api_key", "apikey", "authorization", "credential"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func truncate(value string, maxChars int) string {
	runes := []rune(value)
	if len(runes) <= maxChars {
		return value
	}
	if maxChars <= 3 {
		return string(runes[:maxChars])
	}
	return string(runes[:maxChars-3]) + "..."
}
