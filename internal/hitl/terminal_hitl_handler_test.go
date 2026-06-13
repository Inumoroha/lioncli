package hitl

import (
	"bytes"
	"strings"
	"testing"
)

func TestTerminalHitlHandlerApproveAllPersistsByTool(t *testing.T) {
	var out bytes.Buffer
	handler := NewTerminalHitlHandlerWithIO(true, strings.NewReader("a\n\n"), &out)
	request := NewApprovalRequest("write_file", `{}`, "", "", "")

	result := handler.RequestApproval(request)
	if !result.IsApprovedAllForTool() {
		t.Fatalf("expected approve-all-for-tool, got %v", result.Decision)
	}
	if !handler.IsApprovedAllByTool("write_file") {
		t.Fatal("tool approval should be persisted")
	}

	result = handler.RequestApproval(request)
	if !result.IsApprovedAllForTool() {
		t.Fatalf("expected auto-approved result, got %v", result.Decision)
	}
}

func TestTerminalHitlHandlerModifyArguments(t *testing.T) {
	var out bytes.Buffer
	handler := NewTerminalHitlHandlerWithIO(true, strings.NewReader("m\n{\"path\":\"b.txt\"}\n"), &out)
	request := NewApprovalRequest("write_file", `{"path":"a.txt"}`, "", "", "")

	result := handler.RequestApproval(request)
	if result.Decision != DecisionModified {
		t.Fatalf("expected modified result, got %v", result.Decision)
	}
	if result.ModifiedArguments != `{"path":"b.txt"}` {
		t.Fatalf("unexpected modified args: %s", result.ModifiedArguments)
	}
}
