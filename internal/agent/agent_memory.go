package agent

// 记忆子系统的对外访问器:供 TUI 显示状态、执行 /memory 命令。
// 全部 nil 安全——memMgr 未装配(降级)时返回提示/空,不 panic。

// MemoryEnabled 报告记忆子系统是否启用。
func (a *Agent) MemoryEnabled() bool {
	return a.memMgr != nil
}

// MemoryStatus 返回多行的记忆/上下文状态(策略、短期/长期用量、token 账)。
func (a *Agent) MemoryStatus() string {
	if a.memMgr == nil {
		return "记忆未启用"
	}
	return a.memMgr.SystemStatus()
}

// MemoryFacts 返回长期记忆里的事实内容列表(每条一行正文)。
func (a *Agent) MemoryFacts() []string {
	if a.memMgr == nil {
		return nil
	}
	entries := a.memMgr.LongTermMemory().GetAll()
	facts := make([]string, 0, len(entries))
	for _, e := range entries {
		facts = append(facts, e.Content)
	}
	return facts
}

// ClearShortTermMemory 清空当前会话的短期记忆。
func (a *Agent) ClearShortTermMemory() {
	if a.memMgr != nil {
		a.memMgr.ClearShortTerm()
	}
}

// ClearLongTermMemory 清空持久化的长期记忆(事实)。
func (a *Agent) ClearLongTermMemory() {
	if a.memMgr != nil {
		a.memMgr.ClearLongTerm()
	}
}

// TokenUsageCompact 返回一行紧凑 token 用量,供 TUI header 展示;未启用时为空串。
func (a *Agent) TokenUsageCompact() string {
	if a.memMgr == nil {
		return ""
	}
	return a.memMgr.TokenUsageCompact()
}
