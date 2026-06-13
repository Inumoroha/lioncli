package hitl

import "sync"

type approvalState struct {
	mu                  sync.RWMutex
	enabled             bool
	approvedAllByTool   map[string]struct{}
	approvedAllByServer map[string]struct{}
}

func newApprovalState(enabled bool) approvalState {
	return approvalState{
		enabled:             enabled,
		approvedAllByTool:   make(map[string]struct{}),
		approvedAllByServer: make(map[string]struct{}),
	}
}

func (s *approvalState) isEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.enabled
}

func (s *approvalState) setEnabled(enabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enabled = enabled
}

func (s *approvalState) isApprovedAllByTool(toolName string) bool {
	if toolName == "" {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.approvedAllByTool[toolName]
	return ok
}

func (s *approvalState) approveTool(toolName string) {
	if toolName == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.approvedAllByTool[toolName] = struct{}{}
}

func (s *approvalState) isApprovedAllByServer(serverName string) bool {
	if serverName == "" {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.approvedAllByServer[serverName]
	return ok
}

func (s *approvalState) approveServer(serverName string) {
	if serverName == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.approvedAllByServer[serverName] = struct{}{}
}

func (s *approvalState) clearApprovedAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.approvedAllByTool = make(map[string]struct{})
	s.approvedAllByServer = make(map[string]struct{})
}

func (s *approvalState) clearApprovedAllForServer(serverName string) {
	if serverName == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.approvedAllByServer, serverName)
}
