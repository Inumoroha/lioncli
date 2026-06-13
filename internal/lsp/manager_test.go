package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunPostEditHookReportsGoParseErrors(t *testing.T) {
	t.Setenv("TEACLI_LSP_ENABLED", "")
	t.Setenv("PAICLI_LSP_ENABLED", "")
	t.Setenv("TEACLI_LSP_PACKAGE_CHECKS", "false")

	dir := t.TempDir()
	path := filepath.Join(dir, "broken.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc main() {\n\tfmt.Println(\n}\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	report := NewManager().RunPostEditHook("broken.go", path)
	if report.IsEmpty() {
		t.Fatal("expected diagnostics for invalid Go source")
	}
	for _, want := range []string{
		"[LSP diagnostics]",
		"[error] broken.go:",
		"go/parser",
	} {
		if !strings.Contains(report.PromptText, want) {
			t.Fatalf("diagnostic report missing %q:\n%s", want, report.PromptText)
		}
	}
}

func TestRunPostEditHookSkipsValidGoFiles(t *testing.T) {
	t.Setenv("TEACLI_LSP_ENABLED", "")
	t.Setenv("PAICLI_LSP_ENABLED", "")
	t.Setenv("TEACLI_LSP_PACKAGE_CHECKS", "false")

	dir := t.TempDir()
	path := filepath.Join(dir, "valid.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	report := NewManager().RunPostEditHook("valid.go", path)
	if !report.IsEmpty() {
		t.Fatalf("expected no diagnostics, got:\n%s", report.PromptText)
	}
}

func TestRunPostEditHookCanBeDisabled(t *testing.T) {
	t.Setenv("TEACLI_LSP_ENABLED", "false")
	t.Setenv("PAICLI_LSP_ENABLED", "")

	dir := t.TempDir()
	path := filepath.Join(dir, "broken.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc main() {\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	report := NewManager().RunPostEditHook("broken.go", path)
	if !report.IsEmpty() {
		t.Fatalf("expected diagnostics to be disabled, got:\n%s", report.PromptText)
	}
}

func TestRunPostEditHookReportsGoPackageErrors(t *testing.T) {
	t.Setenv("TEACLI_LSP_ENABLED", "")
	t.Setenv("PAICLI_LSP_ENABLED", "")
	t.Setenv("TEACLI_LSP_PACKAGE_CHECKS", "")

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.test/broken\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc main() {\n\tmissingSymbol()\n}\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	report := NewManager().RunPostEditHook("main.go", path)
	if report.IsEmpty() {
		t.Fatal("expected go vet diagnostics")
	}
	for _, want := range []string{"[LSP diagnostics]", "go vet", "missingSymbol"} {
		if !strings.Contains(report.PromptText, want) {
			t.Fatalf("diagnostic report missing %q:\n%s", want, report.PromptText)
		}
	}
}

func TestRunPostEditHookReportsJSONErrors(t *testing.T) {
	t.Setenv("TEACLI_LSP_ENABLED", "")
	t.Setenv("PAICLI_LSP_ENABLED", "")

	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("{\n  \"name\": \n}\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	report := NewManager().RunPostEditHook("bad.json", path)
	if report.IsEmpty() {
		t.Fatal("expected JSON diagnostics")
	}
	for _, want := range []string{"[LSP diagnostics]", "bad.json:", "json"} {
		if !strings.Contains(report.PromptText, want) {
			t.Fatalf("diagnostic report missing %q:\n%s", want, report.PromptText)
		}
	}
}
