package lsp

type Severity int

const (
	SeverityError Severity = iota
	SeverityWarning
	SeverityInfo
)

type Diagnostic struct {
	Severity Severity
	FilePath string
	Line     int
	Column   int
	Message  string
	Source   string
}

func NewDiagnostic(severity Severity, filePath string, line, column int, message, source string) Diagnostic {
	if line < 1 {
		line = 1
	}
	if column < 1 {
		column = 1
	}
	if source == "" {
		source = "lsp"
	}
	return Diagnostic{
		Severity: severity,
		FilePath: filePath,
		Line:     line,
		Column:   column,
		Message:  message,
		Source:   source,
	}
}
