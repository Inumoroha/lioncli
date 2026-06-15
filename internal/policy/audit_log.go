package policy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	ApproverHITL    = "hitl"
	ApproverPolicy  = "policy"
	ApproverNone    = "none"
	ApproverMention = "mention"

	OutcomeAllow = "allow"
	OutcomeDeny  = "deny"
	OutcomeError = "error"

	maxAuditFieldChars = 1000
)

type AuditLog struct {
	dir string
	mu  sync.Mutex
}

type AuditEntry struct {
	Timestamp  string `json:"timestamp"`
	Tool       string `json:"tool"`
	Category   string `json:"category,omitempty"`
	Mutates    bool   `json:"mutates,omitempty"`
	Args       string `json:"args,omitempty"`
	Outcome    string `json:"outcome"`
	Reason     string `json:"reason,omitempty"`
	Approver   string `json:"approver"`
	DurationMS int64  `json:"duration_ms"`
}

func NewAuditLog(dir string) *AuditLog {
	if strings.TrimSpace(dir) == "" {
		dir = defaultAuditDir()
	}
	return &AuditLog{dir: dir}
}

func DefaultAuditLog() *AuditLog {
	return NewAuditLog("")
}

func (l *AuditLog) Dir() string {
	return l.dir
}

func (l *AuditLog) Record(entry AuditEntry) {
	if l == nil {
		return
	}
	if entry.Timestamp == "" {
		entry.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if entry.Approver == "" {
		entry.Approver = ApproverNone
	}
	if entry.Category == "" {
		entry.Category, entry.Mutates = classifyAuditTool(entry.Tool)
	}
	entry.Args = truncateAuditField(entry.Args)
	entry.Reason = truncateAuditField(entry.Reason)

	l.mu.Lock()
	defer l.mu.Unlock()

	if err := os.MkdirAll(l.dir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "warn: audit log unavailable: %v\n", err)
		return
	}
	file := filepath.Join(l.dir, "audit-"+time.Now().Format("2006-01-02")+".jsonl")
	f, err := os.OpenFile(file, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warn: audit log unavailable: %v\n", err)
		return
	}
	defer f.Close()

	encoded, err := json.Marshal(entry)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warn: audit log encode failed: %v\n", err)
		return
	}
	if _, err := f.Write(append(encoded, '\n')); err != nil {
		fmt.Fprintf(os.Stderr, "warn: audit log write failed: %v\n", err)
	}
}

func Allow(tool, args string, duration time.Duration) AuditEntry {
	return AuditEntry{Tool: tool, Args: args, Outcome: OutcomeAllow, Approver: ApproverNone, DurationMS: duration.Milliseconds()}
}

func DenyByHITL(tool, args, reason string, duration time.Duration) AuditEntry {
	return AuditEntry{Tool: tool, Args: args, Outcome: OutcomeDeny, Reason: reason, Approver: ApproverHITL, DurationMS: duration.Milliseconds()}
}

func DenyByPolicy(tool, args, reason string, duration time.Duration) AuditEntry {
	return AuditEntry{Tool: tool, Args: args, Outcome: OutcomeDeny, Reason: reason, Approver: ApproverPolicy, DurationMS: duration.Milliseconds()}
}

func ToolError(tool, args, reason string, duration time.Duration) AuditEntry {
	return AuditEntry{Tool: tool, Args: args, Outcome: OutcomeError, Reason: reason, Approver: ApproverNone, DurationMS: duration.Milliseconds()}
}

func classifyAuditTool(tool string) (string, bool) {
	switch {
	case strings.HasPrefix(tool, "mcp__"):
		return "mcp", true
	case tool == "apply_patch" || tool == "write_file":
		return "file_edit", true
	case tool == "command_execute":
		// 本项目执行 shell 的工具名为 command_execute(不是 execute_command)。
		// 拼错会让 shell 执行被误分类成普通 tool、mutates 标成 false。
		return "command", true
	case tool == "git_commit":
		return "git", true
	case strings.HasPrefix(tool, "git_"):
		return "git", false
	case tool == "read_file" || tool == "code_search":
		return "read", false
	case strings.HasPrefix(tool, "web_"):
		return "web", false
	default:
		return "tool", false
	}
}

func defaultAuditDir() string {
	if env := strings.TrimSpace(os.Getenv("LIONCLI_AUDIT_DIR")); env != "" {
		return env
	}
	// 兼容旧项目名(teacli)时期设置的环境变量。
	if env := strings.TrimSpace(os.Getenv("TEACLI_AUDIT_DIR")); env != "" {
		return env
	}
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "lioncli", "audit")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".lioncli", "audit")
	}
	return filepath.Join(".", ".lioncli", "audit")
}

func truncateAuditField(s string) string {
	if s == "" {
		return ""
	}
	s = sanitizeAuditField(s)
	if len(s) <= maxAuditFieldChars {
		return s
	}
	return s[:maxAuditFieldChars] + "...(truncated)"
}

var (
	bearerPattern = regexp.MustCompile(`(?i)Bearer\s+[^\s"'}]+`)
	jsonSecretPat = regexp.MustCompile(`(?i)("?(?:token|key|password|secret|authorization)"?\s*[:=]\s*")([^"]+)(")`)
	kvSecretPat   = regexp.MustCompile(`(?i)(\b(?:token|key|password|secret|authorization)\b\s*[:=]\s*)([^\s,}]+)`)
)

func sanitizeAuditField(s string) string {
	s = bearerPattern.ReplaceAllString(s, "Bearer ***")
	s = jsonSecretPat.ReplaceAllString(s, "$1***$3")
	s = kvSecretPat.ReplaceAllString(s, "$1***")
	return s
}
