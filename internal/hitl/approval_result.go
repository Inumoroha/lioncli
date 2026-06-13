package hitl

type ApprovalDecision string

const (
	DecisionApproved            ApprovalDecision = "APPROVED"
	DecisionApprovedAll         ApprovalDecision = "APPROVED_ALL"
	DecisionApprovedAllByServer ApprovalDecision = "APPROVED_ALL_BY_SERVER"
	DecisionRejected            ApprovalDecision = "REJECTED"
	DecisionModified            ApprovalDecision = "MODIFIED"
	DecisionSkipped             ApprovalDecision = "SKIPPED"
)

type ApprovalResult struct {
	Decision          ApprovalDecision
	ModifiedArguments string
	Reason            string
}

func Approve() ApprovalResult {
	return ApprovalResult{Decision: DecisionApproved}
}

func ApproveAll() ApprovalResult {
	return ApprovalResult{Decision: DecisionApprovedAll}
}

func ApproveAllByServer() ApprovalResult {
	return ApprovalResult{Decision: DecisionApprovedAllByServer}
}

func Reject(reason string) ApprovalResult {
	return ApprovalResult{Decision: DecisionRejected, Reason: reason}
}

func Modify(modifiedArguments string) ApprovalResult {
	return ApprovalResult{Decision: DecisionModified, ModifiedArguments: modifiedArguments}
}

func Skip() ApprovalResult {
	return ApprovalResult{Decision: DecisionSkipped}
}

func (r ApprovalResult) IsApproved() bool {
	return r.Decision == DecisionApproved ||
		r.Decision == DecisionApprovedAll ||
		r.Decision == DecisionApprovedAllByServer ||
		r.Decision == DecisionModified
}

func (r ApprovalResult) IsApprovedAll() bool {
	return r.Decision == DecisionApprovedAll
}

func (r ApprovalResult) IsApprovedAllForTool() bool {
	return r.Decision == DecisionApprovedAll
}

func (r ApprovalResult) IsApprovedAllForServer() bool {
	return r.Decision == DecisionApprovedAllByServer
}

func (r ApprovalResult) IsRejected() bool {
	return r.Decision == DecisionRejected
}

func (r ApprovalResult) IsSkipped() bool {
	return r.Decision == DecisionSkipped
}

func (r ApprovalResult) EffectiveArguments(originalArguments string) string {
	if r.Decision == DecisionModified && r.ModifiedArguments != "" {
		return r.ModifiedArguments
	}
	return originalArguments
}
