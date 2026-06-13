package hitl

type HitlHandler interface {
	RequestApproval(request ApprovalRequest) ApprovalResult
	IsEnabled() bool
	SetEnabled(enabled bool)
	IsApprovedAllByTool(toolName string) bool
	IsApprovedAllByServer(serverName string) bool
	ClearApprovedAll()
	ClearApprovedAllForServer(serverName string)
}
