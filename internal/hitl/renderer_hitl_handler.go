package hitl

import (
	"fmt"
	"io"
	"sync"
)

type ApprovalRenderer interface {
	PromptApproval(request ApprovalRequest) ApprovalResult
	Stream() io.Writer
}

type RendererHitlHandler struct {
	renderer  ApprovalRenderer
	state     approvalState
	requestMu sync.Mutex
}

func NewRendererHitlHandler(renderer ApprovalRenderer, enabled bool) *RendererHitlHandler {
	if renderer == nil {
		panic("renderer is nil")
	}
	return &RendererHitlHandler{
		renderer: renderer,
		state:    newApprovalState(enabled),
	}
}

func (h *RendererHitlHandler) RequestApproval(request ApprovalRequest) ApprovalResult {
	h.requestMu.Lock()
	defer h.requestMu.Unlock()

	mcpServer := MCPServerName(request.ToolName)
	sensitivePerCall := request.SensitiveNotice != ""

	if !sensitivePerCall && h.state.isApprovedAllByTool(request.ToolName) {
		fmt.Fprintf(h.renderer.Stream(), "  [HITL] %s is already approved for the rest of this session\n", request.ToolName)
		return ApproveAll()
	}
	if !sensitivePerCall && h.state.isApprovedAllByServer(mcpServer) {
		fmt.Fprintf(h.renderer.Stream(), "  [HITL] MCP server %s is already approved for the rest of this session\n", mcpServer)
		return ApproveAllByServer()
	}

	result := h.renderer.PromptApproval(request)
	if result.Decision == "" {
		return Reject("renderer returned an empty decision")
	}
	if result.IsApprovedAllForTool() {
		h.state.approveTool(request.ToolName)
	} else if result.IsApprovedAllForServer() {
		h.state.approveServer(mcpServer)
	}
	return result
}

func (h *RendererHitlHandler) IsEnabled() bool {
	return h.state.isEnabled()
}

func (h *RendererHitlHandler) SetEnabled(enabled bool) {
	h.state.setEnabled(enabled)
}

func (h *RendererHitlHandler) IsApprovedAllByTool(toolName string) bool {
	return h.state.isApprovedAllByTool(toolName)
}

func (h *RendererHitlHandler) IsApprovedAllByServer(serverName string) bool {
	return h.state.isApprovedAllByServer(serverName)
}

func (h *RendererHitlHandler) ClearApprovedAll() {
	h.state.clearApprovedAll()
}

func (h *RendererHitlHandler) ClearApprovedAllForServer(serverName string) {
	h.state.clearApprovedAllForServer(serverName)
}
