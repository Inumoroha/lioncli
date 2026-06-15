package policy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAuditLogRecordWritesSanitizedJSONL(t *testing.T) {
	dir := t.TempDir()
	log := NewAuditLog(dir)

	log.Record(Allow("web_fetch", `{"authorization":"Bearer secret-token","password":"p"}`, time.Millisecond))

	files, err := filepath.Glob(filepath.Join(dir, "audit-*.jsonl"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected one audit file, got %d", len(files))
	}
	raw, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("read audit file: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, `"outcome":"allow"`) {
		t.Fatalf("missing allow outcome: %s", text)
	}
	if !strings.Contains(text, `"category":"web"`) {
		t.Fatalf("missing tool category: %s", text)
	}
	if strings.Contains(text, "secret-token") || strings.Contains(text, `"password":"p"`) {
		t.Fatalf("audit log was not sanitized: %s", text)
	}
}

func TestAuditLogClassifiesMutatingTools(t *testing.T) {
	dir := t.TempDir()
	log := NewAuditLog(dir)

	log.Record(Allow("apply_patch", `{"path":"a.go"}`, time.Millisecond))

	files, err := filepath.Glob(filepath.Join(dir, "audit-*.jsonl"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	raw, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("read audit file: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, `"category":"file_edit"`) || !strings.Contains(text, `"mutates":true`) {
		t.Fatalf("audit log missing mutation classification: %s", text)
	}
}

// TestAuditLogClassifiesCommandExecute 是回归用例:本项目执行 shell 的工具名是
// command_execute(不是 execute_command),分类应为 command 且 mutates=true。
func TestAuditLogClassifiesCommandExecute(t *testing.T) {
	if cat, mut := classifyAuditTool("command_execute"); cat != "command" || !mut {
		t.Fatalf("command_execute classified as (%q, %v), want (command, true)", cat, mut)
	}
	// 旧的拼写不是真实工具,不应被识别为 shell 命令。
	if cat, mut := classifyAuditTool("execute_command"); cat == "command" || mut {
		t.Fatalf("execute_command (not a real tool) misclassified as (%q, %v)", cat, mut)
	}
}
