package hitl

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

type TerminalHitlHandler struct {
	state     approvalState
	in        *bufio.Reader
	out       io.Writer
	requestMu sync.Mutex
}

func NewTerminalHitlHandler(enabled bool) *TerminalHitlHandler {
	return NewTerminalHitlHandlerWithIO(enabled, os.Stdin, os.Stdout)
}

func NewTerminalHitlHandlerWithIO(enabled bool, in io.Reader, out io.Writer) *TerminalHitlHandler {
	if in == nil {
		in = os.Stdin
	}
	if out == nil {
		out = os.Stdout
	}
	return &TerminalHitlHandler{
		state: newApprovalState(enabled),
		in:    bufio.NewReader(in),
		out:   out,
	}
}

func (h *TerminalHitlHandler) IsEnabled() bool {
	return h.state.isEnabled()
}

func (h *TerminalHitlHandler) SetEnabled(enabled bool) {
	h.state.setEnabled(enabled)
}

func (h *TerminalHitlHandler) RequestApproval(request ApprovalRequest) ApprovalResult {
	h.requestMu.Lock()
	defer h.requestMu.Unlock()

	mcpServer := MCPServerName(request.ToolName)
	sensitivePerCall := request.SensitiveNotice != ""

	if !sensitivePerCall && h.state.isApprovedAllByTool(request.ToolName) {
		fmt.Fprintf(h.out, "  [HITL] %s is already approved for the rest of this session\n", request.ToolName)
		return ApproveAll()
	}
	if !sensitivePerCall && h.state.isApprovedAllByServer(mcpServer) {
		fmt.Fprintf(h.out, "  [HITL] MCP server %s is already approved for the rest of this session\n", mcpServer)
		return ApproveAllByServer()
	}

	fmt.Fprintln(h.out)
	fmt.Fprintln(h.out, "========== HITL Approval Request ==========")
	if sensitivePerCall {
		fmt.Fprintf(h.out, "Sensitive notice: %s\n", request.SensitiveNotice)
	}
	fmt.Fprintln(h.out, request.DisplayText())

	return h.promptUntilDecision(request)
}

func (h *TerminalHitlHandler) promptUntilDecision(request ApprovalRequest) ApprovalResult {
	sensitivePerCall := request.SensitiveNotice != ""

	for attempt := 0; attempt < 5; attempt++ {
		fmt.Fprintln(h.out)
		if sensitivePerCall {
			fmt.Fprintln(h.out, "Choose: [y/Enter] approve once  [n] reject  [s] skip  [m] modify arguments")
		} else {
			fmt.Fprintln(h.out, "Choose: [y/Enter] approve  [a] approve all  [n] reject  [s] skip  [m] modify arguments")
		}
		fmt.Fprint(h.out, "> ")

		input, err := h.readLine()
		if err != nil {
			fmt.Fprintf(h.out, "  [HITL] failed to read user input, rejecting by default: %v\n", err)
			return Reject("failed to read user input: " + err.Error())
		}

		switch normalized := strings.ToLower(strings.TrimSpace(input)); normalized {
		case "", "y":
			fmt.Fprintln(h.out, "  Approved")
			return Approve()
		case "a":
			if sensitivePerCall {
				fmt.Fprintln(h.out, "  Sensitive approvals do not support approve-all. Use y/n/s/m.")
				continue
			}
			return h.promptApproveAllScope(request)
		case "n":
			fmt.Fprint(h.out, "  Reject reason (optional): ")
			reason, err := h.readLine()
			if err != nil {
				reason = ""
			}
			return Reject(strings.TrimSpace(reason))
		case "s":
			fmt.Fprintln(h.out, "  Skipped")
			return Skip()
		case "m":
			if modified, ok := h.promptModifiedArguments(request); ok {
				return modified
			}
		default:
			fmt.Fprintf(h.out, "  Unknown choice %q. Use y/a/n/s/m.\n", input)
		}
	}

	fmt.Fprintln(h.out, "  [HITL] too many invalid attempts, rejecting by default")
	return Reject("too many invalid inputs")
}

func (h *TerminalHitlHandler) promptApproveAllScope(request ApprovalRequest) ApprovalResult {
	mcpServer := MCPServerName(request.ToolName)
	if mcpServer == "" {
		h.state.approveTool(request.ToolName)
		fmt.Fprintf(h.out, "  Approved. Future %s calls will auto-approve in this session.\n", request.ToolName)
		return ApproveAll()
	}

	fmt.Fprintln(h.out, "  Approve-all scope:")
	fmt.Fprintf(h.out, "  [tool/Enter] only %s\n", request.ToolName)
	fmt.Fprintf(h.out, "  [server]     the whole MCP server %s\n", mcpServer)
	fmt.Fprint(h.out, "> ")

	scope, err := h.readLine()
	if err != nil {
		scope = ""
	}
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "server", "s":
		h.state.approveServer(mcpServer)
		fmt.Fprintf(h.out, "  Approved. Future MCP calls for server %s will auto-approve in this session.\n", mcpServer)
		return ApproveAllByServer()
	default:
		h.state.approveTool(request.ToolName)
		fmt.Fprintf(h.out, "  Approved. Future %s calls will auto-approve in this session.\n", request.ToolName)
		return ApproveAll()
	}
}

func (h *TerminalHitlHandler) promptModifiedArguments(request ApprovalRequest) (ApprovalResult, bool) {
	fmt.Fprintf(h.out, "  Current arguments: %s\n", request.Arguments)
	fmt.Fprint(h.out, "  Enter modified JSON arguments (blank keeps the original arguments): ")

	modified, err := h.readLine()
	if err != nil {
		fmt.Fprintln(h.out, "  Failed to read modified arguments. Returning to the main prompt.")
		return ApprovalResult{}, false
	}

	trimmed := strings.TrimSpace(modified)
	if trimmed == "" {
		fmt.Fprintln(h.out, "  Empty input. Approving the original arguments.")
		return Approve(), true
	}
	if !json.Valid([]byte(trimmed)) {
		fmt.Fprintln(h.out, "  Modified arguments are not valid JSON. Returning to the main prompt.")
		return ApprovalResult{}, false
	}
	return Modify(trimmed), true
}

func (h *TerminalHitlHandler) readLine() (string, error) {
	line, err := h.in.ReadString('\n')
	if err != nil {
		if errors.Is(err, io.EOF) && line != "" {
			return strings.TrimRight(line, "\r\n"), nil
		}
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func (h *TerminalHitlHandler) ClearApprovedAll() {
	h.state.clearApprovedAll()
}

func (h *TerminalHitlHandler) ClearApprovedAllForServer(serverName string) {
	h.state.clearApprovedAllForServer(serverName)
}

func (h *TerminalHitlHandler) IsApprovedAllByTool(toolName string) bool {
	return h.state.isApprovedAllByTool(toolName)
}

func (h *TerminalHitlHandler) IsApprovedAllByServer(serverName string) bool {
	return h.state.isApprovedAllByServer(serverName)
}
