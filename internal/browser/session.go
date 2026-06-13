package browser

import "sync"

// Session 持有当前浏览器会话状态。由宿主(TUI/main)持有并注入 Guard,
// 不做全局单例,便于测试与多会话。所有方法并发安全。
type Session struct {
	mu               sync.RWMutex
	mode             Mode
	browserURL       string
	lastNavigatedURL string
	agentOpenedTabs  map[string]struct{}
}

// NewSession 创建一个默认 ISOLATED 模式的会话。
func NewSession() *Session {
	return &Session{
		mode:            ModeIsolated,
		agentOpenedTabs: make(map[string]struct{}),
	}
}

func (s *Session) Mode() Mode {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mode
}

func (s *Session) BrowserURL() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.browserURL
}

func (s *Session) LastNavigatedURL() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastNavigatedURL
}

// SwitchToIsolated 切回独立模式并清空会话内的导航/标签记录。
func (s *Session) SwitchToIsolated() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mode = ModeIsolated
	s.browserURL = ""
	s.lastNavigatedURL = ""
	s.agentOpenedTabs = make(map[string]struct{})
}

// SwitchToShared 切到接管已有 Chrome 的共享模式。
func (s *Session) SwitchToShared(browserURL string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mode = ModeShared
	s.browserURL = browserURL
	s.lastNavigatedURL = ""
	s.agentOpenedTabs = make(map[string]struct{})
}

// RememberNavigation 记录最近一次导航到的 URL,供后续不带 URL 的工具(click/fill 等)
// 判定所处页面是否敏感。
func (s *Session) RememberNavigation(url string) {
	if url == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastNavigatedURL = url
}

// RecordOpenedTab 记录由 agent 打开的标签页 id;SHARED 模式据此区分用户自己的标签页。
func (s *Session) RecordOpenedTab(pageID string) {
	if pageID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agentOpenedTabs[pageID] = struct{}{}
}

func (s *Session) IsAgentOpenedTab(pageID string) bool {
	if pageID == "" {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.agentOpenedTabs[pageID]
	return ok
}
