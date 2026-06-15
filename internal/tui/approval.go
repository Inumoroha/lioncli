package tui

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"

	"lioncli/internal/hitl"
)

func sensitivePatternsPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "teacli", "sensitive_patterns.txt")
}

type approvalBridge struct {
	prompts   chan hitl.ApprovalRequest
	decisions chan hitl.ApprovalResult
}

func newApprovalBridge() *approvalBridge {
	return &approvalBridge{
		prompts:   make(chan hitl.ApprovalRequest),
		decisions: make(chan hitl.ApprovalResult),
	}
}

func (b *approvalBridge) PromptApproval(req hitl.ApprovalRequest) hitl.ApprovalResult {
	b.prompts <- req
	return <-b.decisions
}

func (b *approvalBridge) Stream() io.Writer {
	return io.Discard
}

type approvalMsg hitl.ApprovalRequest

func (b *approvalBridge) waitForApproval() tea.Cmd {
	return func() tea.Msg {
		return approvalMsg(<-b.prompts)
	}
}

func (b *approvalBridge) sendDecision(res hitl.ApprovalResult) tea.Cmd {
	return func() tea.Msg {
		b.decisions <- res
		return nil
	}
}

func decodeApprovalKey(msg tea.KeyMsg, req hitl.ApprovalRequest) (hitl.ApprovalResult, bool) {
	if msg.Type == tea.KeyEnter {
		return hitl.Approve(), true
	}
	if msg.Type != tea.KeyRunes || len(msg.Runes) == 0 {
		return hitl.ApprovalResult{}, false
	}
	switch unicode.ToLower(msg.Runes[0]) {
	case 'y':
		return hitl.Approve(), true
	case 'a':
		return hitl.ApproveAll(), true
	case 's':
		if hitl.IsMCPTool(req.ToolName) {
			return hitl.ApproveAllByServer(), true
		}
		return hitl.ApprovalResult{}, false
	case 'n':
		return hitl.Reject(""), true
	default:
		return hitl.ApprovalResult{}, false
	}
}

func approvalSummary(req hitl.ApprovalRequest, res hitl.ApprovalResult) string {
	switch {
	case res.IsApprovedAllForServer():
		return fmt.Sprintf("Approved all calls from MCP server [%s] for this session.", hitl.MCPServerName(req.ToolName))
	case res.IsApprovedAllForTool():
		return fmt.Sprintf("Approved tool [%s] for this session.", req.ToolName)
	case res.IsRejected():
		return fmt.Sprintf("Rejected [%s].", req.ToolName)
	default:
		return fmt.Sprintf("Approved [%s].", req.ToolName)
	}
}

func approvalKeyHint(req hitl.ApprovalRequest) string {
	if hitl.IsMCPTool(req.ToolName) {
		return fmt.Sprintf("Approval required for [%s]: Enter/y approve, a always allow this tool, s always allow this server, n reject", req.ToolName)
	}
	return fmt.Sprintf("Approval required for [%s]: Enter/y approve, a always allow this tool, n reject", req.ToolName)
}
