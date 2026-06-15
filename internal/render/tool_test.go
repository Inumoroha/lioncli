package render

import (
	"strings"
	"testing"
)

func TestFormatToolArgsSortsAndTruncates(t *testing.T) {
	got := FormatToolArgs(map[string]any{
		"z": "last",
		"a": strings.Repeat("x", 10),
	}, 5)

	if !strings.HasPrefix(got, "a: xx") {
		t.Fatalf("expected sorted output starting with a, got:\n%s", got)
	}
	if !strings.Contains(got, "...[truncated]") {
		t.Fatalf("expected truncation marker, got:\n%s", got)
	}
}

func TestToolLabelAndKeyParam(t *testing.T) {
	if got := ToolLabel("mcp__chrome-devtools__click", 2); got != "MCP tool chrome-devtools.click x 2" {
		t.Fatalf("unexpected MCP label: %q", got)
	}

	args := map[string]any{"command": "go test ./..."}
	if got := ToolLabel("command_execute", 1); got != "execute 1 command(s)" {
		t.Fatalf("unexpected command label: %q", got)
	}
	if got := KeyParam("command_execute", args, 80); got != "go test ./..." {
		t.Fatalf("unexpected key param: %q", got)
	}
}

func TestFormatToolArgsRedactsSensitiveValues(t *testing.T) {
	got := FormatToolArgs(map[string]any{
		"api_key": "secret-value",
		"path":    "config.json",
	}, 80)

	if strings.Contains(got, "secret-value") {
		t.Fatalf("sensitive value leaked:\n%s", got)
	}
	if !strings.Contains(got, "api_key: [redacted]") || !strings.Contains(got, "path: config.json") {
		t.Fatalf("unexpected redacted args:\n%s", got)
	}
}

func TestFormatToolCallIncludesStateAndDetails(t *testing.T) {
	got := FormatToolCall("command_execute", map[string]any{"command": "go test ./..."}, ToolStatePending, 80)

	for _, want := range []string{"[pending]", "execute 1 command(s): go test ./...", "command: go test ./..."} {
		if !strings.Contains(got, want) {
			t.Fatalf("tool call missing %q:\n%s", want, got)
		}
	}
}
