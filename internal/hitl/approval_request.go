package hitl

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"
)

const (
	boxInnerWidth       = 58
	argLineWidth        = boxInnerWidth - 6
	maxLongValuePreview = 120
)

type ApprovalRequest struct {
	ToolName        string
	Arguments       string
	DangerLevel     string
	RiskDescription string
	Suggestion      string
	CallerContext   string
	SensitiveNotice string
}

func NewApprovalRequest(toolName, arguments, suggestion, callerContext, sensitiveNotice string) ApprovalRequest {
	return ApprovalRequest{
		ToolName:        toolName,
		Arguments:       arguments,
		DangerLevel:     DangerLevel(toolName),
		RiskDescription: RiskDescription(toolName),
		Suggestion:      suggestion,
		CallerContext:   callerContext,
		SensitiveNotice: sensitiveNotice,
	}
}

func (r ApprovalRequest) DisplayText() string {
	var builder strings.Builder
	border := "+" + strings.Repeat("-", boxInnerWidth) + "+"

	builder.WriteString(border)
	builder.WriteByte('\n')
	builder.WriteString(formatBoxLine("Approval Required"))
	builder.WriteByte('\n')
	builder.WriteString(border)
	builder.WriteByte('\n')
	builder.WriteString(formatBoxField("Tool", r.ToolName))
	builder.WriteByte('\n')
	if server := MCPServerName(r.ToolName); server != "" {
		builder.WriteString(formatBoxField("MCP Server", server))
		builder.WriteByte('\n')
	}
	builder.WriteString(formatBoxField("Level", r.DangerLevel))
	builder.WriteByte('\n')
	builder.WriteString(formatBoxField("Risk", r.RiskDescription))
	builder.WriteByte('\n')
	if strings.TrimSpace(r.CallerContext) != "" {
		builder.WriteString(formatBoxField("Source", r.CallerContext))
		builder.WriteByte('\n')
	}
	if strings.TrimSpace(r.SensitiveNotice) != "" {
		builder.WriteString(formatBoxField("Sensitive", r.SensitiveNotice))
		builder.WriteByte('\n')
	}
	builder.WriteString(border)
	builder.WriteByte('\n')
	builder.WriteString(formatBoxLine("Arguments:"))
	builder.WriteByte('\n')
	for _, line := range formatArgs(r.Arguments) {
		builder.WriteString(formatBoxIndented(line))
		builder.WriteByte('\n')
	}
	if strings.TrimSpace(r.Suggestion) != "" {
		builder.WriteString(border)
		builder.WriteByte('\n')
		builder.WriteString(formatBoxLine("Reason:"))
		builder.WriteByte('\n')
		for _, line := range wrapByDisplayWidth(r.Suggestion, argLineWidth) {
			builder.WriteString(formatBoxIndented(line))
			builder.WriteByte('\n')
		}
	}
	builder.WriteString(border)
	return builder.String()
}

func formatBoxField(prefix, value string) string {
	label := prefix + ": "
	target := boxInnerWidth - 2 - displayWidth(label)
	if target < 0 {
		target = 0
	}
	truncated := truncateByDisplayWidth(value, target)
	return "| " + label + padRightByDisplayWidth(truncated, target) + " |"
}

func formatBoxLine(text string) string {
	target := boxInnerWidth - 2
	if target < 0 {
		target = 0
	}
	truncated := truncateByDisplayWidth(text, target)
	return "| " + padRightByDisplayWidth(truncated, target) + " |"
}

func formatBoxIndented(text string) string {
	target := boxInnerWidth - 4
	if target < 0 {
		target = 0
	}
	truncated := truncateByDisplayWidth(text, target)
	return "|   " + padRightByDisplayWidth(truncated, target) + " |"
}

func formatArgs(args string) []string {
	if strings.TrimSpace(args) == "" {
		return []string{"(no arguments)"}
	}

	var root map[string]any
	if err := json.Unmarshal([]byte(args), &root); err != nil {
		return wrapByDisplayWidth(strings.TrimSpace(args), argLineWidth)
	}

	if len(root) == 0 {
		return []string{"(empty object)"}
	}

	keys := make([]string, 0, len(root))
	for key := range root {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		value := root[key]
		switch typed := value.(type) {
		case string:
			if len(typed) > maxLongValuePreview {
				head := strings.ReplaceAll(typed[:maxLongValuePreview], "\n", "\\n")
				lines = append(lines, wrapByDisplayWidth(
					fmt.Sprintf(`%s: "%s..." (%d chars)`, key, head, len(typed)),
					argLineWidth,
				)...)
				continue
			}
			lines = append(lines, wrapByDisplayWidth(
				fmt.Sprintf(`%s: "%s"`, key, strings.ReplaceAll(typed, "\n", "\\n")),
				argLineWidth,
			)...)
		default:
			raw, err := json.Marshal(value)
			if err != nil {
				raw = []byte(fmt.Sprint(value))
			}
			lines = append(lines, wrapByDisplayWidth(
				fmt.Sprintf("%s: %s", key, string(raw)),
				argLineWidth,
			)...)
		}
	}
	return lines
}

func displayWidth(s string) int {
	width := 0
	for _, r := range s {
		if unicode.IsControl(r) {
			continue
		}
		if isWideRune(r) {
			width += 2
			continue
		}
		width++
	}
	return width
}

func isWideRune(r rune) bool {
	return (r >= 0x1100 && r <= 0x115F) ||
		(r >= 0x2E80 && r <= 0x9FFF) ||
		(r >= 0xA000 && r <= 0xA4CF) ||
		(r >= 0xAC00 && r <= 0xD7A3) ||
		(r >= 0xF900 && r <= 0xFAFF) ||
		(r >= 0xFE30 && r <= 0xFE4F) ||
		(r >= 0xFF00 && r <= 0xFF60) ||
		(r >= 0xFFE0 && r <= 0xFFE6) ||
		(r >= 0x2600 && r <= 0x27BF) ||
		(r >= 0x1F300 && r <= 0x1FAFF)
}

func padRightByDisplayWidth(s string, targetCols int) string {
	width := displayWidth(s)
	if width >= targetCols {
		return s
	}
	return s + strings.Repeat(" ", targetCols-width)
}

func truncateByDisplayWidth(s string, targetCols int) string {
	if targetCols <= 0 {
		return ""
	}
	if displayWidth(s) <= targetCols {
		return s
	}

	var builder strings.Builder
	used := 0
	reserve := 3
	for _, r := range s {
		runeWidth := 1
		if isWideRune(r) {
			runeWidth = 2
		}
		if unicode.IsControl(r) {
			runeWidth = 0
		}
		if used+runeWidth > targetCols-reserve {
			break
		}
		builder.WriteRune(r)
		used += runeWidth
	}
	builder.WriteString("...")
	return builder.String()
}

func wrapByDisplayWidth(text string, lineWidth int) []string {
	if strings.TrimSpace(text) == "" {
		return []string{""}
	}

	lines := make([]string, 0, 4)
	var current strings.Builder
	used := 0
	flush := func() {
		lines = append(lines, current.String())
		current.Reset()
		used = 0
	}

	for _, r := range text {
		if r == '\n' {
			flush()
			continue
		}
		runeWidth := 1
		if isWideRune(r) {
			runeWidth = 2
		}
		if unicode.IsControl(r) {
			continue
		}
		if used+runeWidth > lineWidth && current.Len() > 0 {
			flush()
		}
		current.WriteRune(r)
		used += runeWidth
	}
	if current.Len() > 0 {
		flush()
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}
