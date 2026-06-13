package lsp

type DiagnosticReport struct {
	PromptText  string
	DisplayText string
}

func (r DiagnosticReport) IsEmpty() bool {
	return r.PromptText == ""
}
