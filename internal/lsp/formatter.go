package lsp

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const defaultMaxDiagnostics = 20

func Format(diagnostics []Diagnostic) DiagnosticReport {
	if len(diagnostics) == 0 {
		return DiagnosticReport{}
	}

	max := maxDiagnostics()
	count := min(max, len(diagnostics))
	omitted := len(diagnostics) - count

	var prompt strings.Builder
	prompt.WriteString("[LSP diagnostics]\n")
	prompt.WriteString("The agent just edited code and teacli collected these diagnostics. Fix errors first, then warnings.\n\n")

	var display strings.Builder
	display.WriteString("\nLSP diagnostics\n")

	for i := 0; i < count; i++ {
		d := diagnostics[i]
		line := fmt.Sprintf("- [%s] %s:%d:%d %s (%s)",
			severityLabel(d.Severity), d.FilePath, d.Line, d.Column, d.Message, d.Source)
		prompt.WriteString(line)
		prompt.WriteByte('\n')
		display.WriteString(line)
		display.WriteByte('\n')
	}

	if omitted > 0 {
		line := fmt.Sprintf("... %d more diagnostics omitted (limit %d)", omitted, max)
		prompt.WriteString(line)
		prompt.WriteByte('\n')
		display.WriteString(line)
		display.WriteByte('\n')
	}

	return DiagnosticReport{
		PromptText:  strings.TrimSpace(prompt.String()),
		DisplayText: strings.TrimSpace(display.String()),
	}
}

func maxDiagnostics() int {
	raw := os.Getenv("TEACLI_LSP_MAX_DIAGNOSTICS")
	if raw == "" {
		raw = os.Getenv("PAICLI_LSP_MAX_DIAGNOSTICS")
	}
	if raw == "" {
		return defaultMaxDiagnostics
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || parsed <= 0 {
		return defaultMaxDiagnostics
	}
	return parsed
}

func severityLabel(severity Severity) string {
	switch severity {
	case SeverityError:
		return "error"
	case SeverityWarning:
		return "warning"
	case SeverityInfo:
		return "info"
	default:
		return "info"
	}
}
