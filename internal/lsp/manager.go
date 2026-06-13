package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"go/parser"
	"go/scanner"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const packageCheckTimeout = 15 * time.Second

type Manager struct{}

func NewManager() *Manager {
	return &Manager{}
}

func (m *Manager) RunPostEditHook(displayPath string, editedFile string) DiagnosticReport {
	if !enabled() || editedFile == "" {
		return DiagnosticReport{}
	}

	path, err := filepath.Abs(editedFile)
	if err != nil {
		path = editedFile
	}
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		return DiagnosticReport{}
	}

	diagnostics := diagnoseFile(normalizeDisplayPath(displayPath, path), path)
	return Format(diagnostics)
}

func diagnoseFile(displayPath string, file string) []Diagnostic {
	switch strings.ToLower(filepath.Ext(file)) {
	case ".go":
		diagnostics := diagnoseGoSyntax(displayPath, file)
		if len(diagnostics) > 0 || !packageChecksEnabled() {
			return sortDiagnostics(diagnostics)
		}
		return sortDiagnostics(append(diagnostics, diagnoseGoPackage(displayPath, file)...))
	case ".json":
		return sortDiagnostics(diagnoseJSON(displayPath, file))
	case ".mod":
		if filepath.Base(file) == "go.mod" {
			return sortDiagnostics(diagnoseGoMod(displayPath, file))
		}
	case ".xml", ".svg":
		return sortDiagnostics(diagnoseXML(displayPath, file))
	default:
		return nil
	}
	return nil
}

func diagnoseGoSyntax(displayPath string, file string) []Diagnostic {
	fset := token.NewFileSet()
	_, err := parser.ParseFile(fset, file, nil, parser.AllErrors|parser.SkipObjectResolution)
	if err == nil {
		return nil
	}

	var diagnostics []Diagnostic
	if list, ok := err.(scanner.ErrorList); ok {
		for _, scanErr := range list {
			diagnostics = append(diagnostics, NewDiagnostic(
				SeverityError,
				displayPath,
				scanErr.Pos.Line,
				scanErr.Pos.Column,
				cleanMessage(scanErr.Msg),
				"go/parser",
			))
		}
	} else {
		diagnostics = append(diagnostics, NewDiagnostic(
			SeverityError,
			displayPath,
			1,
			1,
			cleanMessage(err.Error()),
			"go/parser",
		))
	}
	return diagnostics
}

func diagnoseGoPackage(displayPath string, file string) []Diagnostic {
	if _, err := exec.LookPath("go"); err != nil {
		return nil
	}
	pkgDir := filepath.Dir(file)
	if !insideGoModule(pkgDir) {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), packageCheckTimeout)
	defer cancel()
	// 用 go vet 而非 go test:vet 会编译并类型检查本包(含测试文件)、捕获编译/类型
	// 错误,但不执行测试、不产出二进制——避免改一个文件就跑整包测试(慢 + 可能触发
	// 测试副作用),也不会像 go build 那样给 main 包留下 stray 可执行文件。
	cmd := exec.CommandContext(ctx, "go", "vet", ".")
	cmd.Dir = pkgDir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return []Diagnostic{NewDiagnostic(SeverityWarning, displayPath, 1, 1, "go vet . timed out", "go vet")}
	}
	if err == nil {
		return nil
	}
	return parseGoPackageDiagnostics(displayPath, pkgDir, out)
}

func diagnoseJSON(displayPath string, file string) []Diagnostic {
	raw, err := os.ReadFile(file)
	if err != nil {
		return []Diagnostic{NewDiagnostic(SeverityError, displayPath, 1, 1, err.Error(), "json")}
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		line, col := jsonOffsetLineColumn(raw, jsonErrorOffset(err))
		return []Diagnostic{NewDiagnostic(SeverityError, displayPath, line, col, cleanMessage(err.Error()), "json")}
	}
	return nil
}

func diagnoseGoMod(displayPath string, file string) []Diagnostic {
	if _, err := exec.LookPath("go"); err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), packageCheckTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "list", "-m")
	cmd.Dir = filepath.Dir(file)
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return []Diagnostic{NewDiagnostic(SeverityWarning, displayPath, 1, 1, "go list -m timed out", "go list")}
	}
	if err == nil {
		return nil
	}
	message := cleanMessage(string(out))
	if message == "" {
		message = err.Error()
	}
	return []Diagnostic{NewDiagnostic(SeverityError, displayPath, 1, 1, message, "go list")}
}

func diagnoseXML(displayPath string, file string) []Diagnostic {
	raw, err := os.ReadFile(file)
	if err != nil {
		return []Diagnostic{NewDiagnostic(SeverityError, displayPath, 1, 1, err.Error(), "xml")}
	}
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	for {
		if _, err := decoder.Token(); err != nil {
			if err.Error() == "EOF" {
				return nil
			}
			line, col := decoder.InputPos()
			return []Diagnostic{NewDiagnostic(SeverityError, displayPath, line, col, cleanMessage(err.Error()), "xml")}
		}
	}
}

func parseGoPackageDiagnostics(displayPath string, pkgDir string, out []byte) []Diagnostic {
	var diagnostics []Diagnostic
	for _, line := range strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n") {
		if d, ok := parseGoDiagnosticLine(displayPath, pkgDir, line); ok {
			diagnostics = append(diagnostics, d)
		}
	}
	if len(diagnostics) == 0 {
		message := cleanMessage(string(out))
		if message == "" {
			message = "go vet . failed"
		}
		diagnostics = append(diagnostics, NewDiagnostic(SeverityError, displayPath, 1, 1, message, "go vet"))
	}
	return diagnostics
}

var goDiagnosticLine = regexp.MustCompile(`^(.+\.go):(\d+):(?:(\d+):)?\s*(.+)$`)

func parseGoDiagnosticLine(displayPath string, pkgDir string, line string) (Diagnostic, bool) {
	match := goDiagnosticLine.FindStringSubmatch(strings.TrimSpace(line))
	if match == nil {
		return Diagnostic{}, false
	}
	lineNo, _ := strconv.Atoi(match[2])
	colNo := 1
	if match[3] != "" {
		colNo, _ = strconv.Atoi(match[3])
	}
	path := match[1]
	if !filepath.IsAbs(path) {
		path = filepath.Join(pkgDir, path)
	}
	if rel, err := filepath.Rel(pkgDir, path); err == nil && !strings.HasPrefix(rel, "..") {
		path = filepath.Join(filepath.Dir(displayPath), rel)
	}
	return NewDiagnostic(SeverityError, filepath.ToSlash(path), lineNo, colNo, cleanMessage(match[4]), "go vet"), true
}

func insideGoModule(dir string) bool {
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

func sortDiagnostics(diagnostics []Diagnostic) []Diagnostic {
	sort.SliceStable(diagnostics, func(i, j int) bool {
		if diagnostics[i].Severity != diagnostics[j].Severity {
			return diagnostics[i].Severity < diagnostics[j].Severity
		}
		if diagnostics[i].FilePath != diagnostics[j].FilePath {
			return diagnostics[i].FilePath < diagnostics[j].FilePath
		}
		if diagnostics[i].Line != diagnostics[j].Line {
			return diagnostics[i].Line < diagnostics[j].Line
		}
		return diagnostics[i].Column < diagnostics[j].Column
	})
	return diagnostics
}

func enabled() bool {
	return envBoolDefault("TEACLI_LSP_ENABLED", "PAICLI_LSP_ENABLED", true)
}

func packageChecksEnabled() bool {
	return envBoolDefault("TEACLI_LSP_PACKAGE_CHECKS", "PAICLI_LSP_PACKAGE_CHECKS", true)
}

func envBoolDefault(primary string, legacy string, def bool) bool {
	raw := os.Getenv(primary)
	if raw == "" {
		raw = os.Getenv(legacy)
	}
	if strings.TrimSpace(raw) == "" {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}

func normalizeDisplayPath(displayPath string, fallback string) string {
	if strings.TrimSpace(displayPath) != "" {
		return filepath.ToSlash(displayPath)
	}
	return filepath.ToSlash(fallback)
}

func cleanMessage(message string) string {
	message = strings.TrimSpace(strings.ReplaceAll(message, "\r\n", "\n"))
	if idx := strings.IndexByte(message, '\n'); idx >= 0 {
		message = strings.TrimSpace(message[:idx])
	}
	if message == "" {
		return "diagnostic error"
	}
	return message
}

func jsonErrorOffset(err error) int64 {
	switch e := err.(type) {
	case *json.SyntaxError:
		return e.Offset
	case *json.UnmarshalTypeError:
		return e.Offset
	default:
		return 1
	}
}

func jsonOffsetLineColumn(raw []byte, offset int64) (int, int) {
	if offset < 1 {
		offset = 1
	}
	line, col := 1, 1
	for i, b := range raw {
		if int64(i+1) >= offset {
			break
		}
		if b == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return line, col
}

func packageCheckCommandForDisplay() string {
	return fmt.Sprintf("go vet . (timeout %s)", packageCheckTimeout)
}
