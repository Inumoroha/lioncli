package render

import (
	"fmt"
	"strings"
)

type StatusInfo struct {
	Model         string
	TotalTokens   int64
	ContextWindow int64
	HITLEnabled   bool
	ElapsedMillis int64
}

func IdleStatus(model string, contextWindow int64, hitlEnabled bool) StatusInfo {
	return StatusInfo{Model: model, ContextWindow: contextWindow, HITLEnabled: hitlEnabled}
}

func FormatStatusLine(info StatusInfo, cols int) string {
	model := strings.TrimSpace(info.Model)
	if model == "" {
		model = "-"
	}
	hitl := "HITL OFF"
	if info.HITLEnabled {
		hitl = "HITL ON"
	}

	line := fmt.Sprintf(" %s | %s/%s | %s",
		model,
		formatTokenCount(info.TotalTokens),
		formatTokenCount(info.ContextWindow),
		hitl,
	)
	if info.ElapsedMillis > 0 {
		line += " | " + formatElapsed(info.ElapsedMillis)
	}
	return truncateColumns(line, cols)
}

func formatTokenCount(tokens int64) string {
	switch {
	case tokens >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(tokens)/1_000_000)
	case tokens >= 1_000:
		return fmt.Sprintf("%.1fk", float64(tokens)/1_000)
	default:
		return fmt.Sprint(tokens)
	}
}

func formatElapsed(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return fmt.Sprintf("%.1fs", float64(ms)/1000)
}

func truncateColumns(value string, cols int) string {
	if cols <= 0 {
		return value
	}
	width := 0
	var b strings.Builder
	for _, r := range value {
		rw := runeWidth(r)
		if width+rw > cols {
			break
		}
		b.WriteRune(r)
		width += rw
	}
	return b.String()
}

func runeWidth(r rune) int {
	if r == '\t' {
		return 4
	}
	if r < 0x20 || (r >= 0x7f && r < 0xa0) {
		return 0
	}
	if isWideRune(r) {
		return 2
	}
	return 1
}

func isWideRune(r rune) bool {
	return (r >= 0x1100 && r <= 0x115f) ||
		(r >= 0x2329 && r <= 0x232a) ||
		(r >= 0x2e80 && r <= 0xa4cf) ||
		(r >= 0xac00 && r <= 0xd7a3) ||
		(r >= 0xf900 && r <= 0xfaff) ||
		(r >= 0xfe10 && r <= 0xfe19) ||
		(r >= 0xfe30 && r <= 0xfe6f) ||
		(r >= 0xff00 && r <= 0xff60) ||
		(r >= 0xffe0 && r <= 0xffe6)
}
