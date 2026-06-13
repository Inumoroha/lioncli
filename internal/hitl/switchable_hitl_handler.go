package hitl

import "sync"

type SwitchableHitlHandler struct {
	mu       sync.RWMutex
	delegate HitlHandler
}

func NewSwitchableHitlHandler(delegate HitlHandler) *SwitchableHitlHandler {
	if delegate == nil {
		panic("delegate is nil")
	}
	return &SwitchableHitlHandler{delegate: delegate}
}

func (h *SwitchableHitlHandler) SetDelegate(delegate HitlHandler) {
	if delegate == nil {
		panic("delegate is nil")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.delegate = delegate
}

func (h *SwitchableHitlHandler) Delegate() HitlHandler {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.delegate
}

func (h *SwitchableHitlHandler) RequestApproval(request ApprovalRequest) ApprovalResult {
	return h.Delegate().RequestApproval(request)
}

func (h *SwitchableHitlHandler) IsEnabled() bool {
	return h.Delegate().IsEnabled()
}

func (h *SwitchableHitlHandler) SetEnabled(enabled bool) {
	h.Delegate().SetEnabled(enabled)
}

func (h *SwitchableHitlHandler) IsApprovedAllByTool(toolName string) bool {
	return h.Delegate().IsApprovedAllByTool(toolName)
}

func (h *SwitchableHitlHandler) IsApprovedAllByServer(serverName string) bool {
	return h.Delegate().IsApprovedAllByServer(serverName)
}

func (h *SwitchableHitlHandler) ClearApprovedAll() {
	h.Delegate().ClearApprovedAll()
}

func (h *SwitchableHitlHandler) ClearApprovedAllForServer(serverName string) {
	h.Delegate().ClearApprovedAllForServer(serverName)
}
