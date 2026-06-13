package browser

// AuditMetadata 是一次浏览器操作检查产生的审计信息,供宿主记录/排查。
type AuditMetadata struct {
	BrowserMode string `json:"browser_mode"`
	Sensitive   bool   `json:"sensitive"`
	TargetURL   string `json:"target_url"`
}

// AuditSink 是审计信息的落地端。宿主(TUI/main)实现它,把每次浏览器工具检查
// 产生的审计写进日志/文件/上报。为空时 Guard 不落地审计(仅在 Decision 里返回)。
type AuditSink interface {
	RecordBrowserAudit(toolName string, meta AuditMetadata)
}
