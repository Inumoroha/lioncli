package hitl

import (
	"strings"
	"testing"
)

func TestApprovalRequestDisplayText(t *testing.T) {
	req := NewApprovalRequest(
		"write_file",
		`{"path":"demo.txt","content":"`+strings.Repeat("x", 130)+`"}`,
		"Need to update the generated file",
		"planner",
		"",
	)

	text := req.DisplayText()
	for _, fragment := range []string{
		"Approval Required",
		"Tool: write_file",
		`path: "demo.txt"`,
		"(130 chars)",
		"Reason:",
		"planner",
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("display text missing %q:\n%s", fragment, text)
		}
	}
}
