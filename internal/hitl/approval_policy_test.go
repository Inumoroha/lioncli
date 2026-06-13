package hitl

import (
	"reflect"
	"testing"
)

func TestApprovalPolicy(t *testing.T) {
	// 关键回归:本项目执行 shell 的工具叫 command_execute(不是 execute_command)。
	// 拼错会让最危险的工具漏审被静默放行。
	if !RequiresApproval("command_execute") {
		t.Fatal("command_execute (shell) must require approval")
	}
	if !RequiresApproval("write_file") {
		t.Fatal("write_file should require approval")
	}
	if !RequiresApproval("mcp__browser__click") {
		t.Fatal("mcp tools should require approval")
	}
	if RequiresApproval("read_file") {
		t.Fatal("read_file should not require approval")
	}
	// 旧名单里的拼写 execute_command 不是真实工具,绝不应被当成危险工具。
	if RequiresApproval("execute_command") {
		t.Fatal("execute_command is not a real tool and must not match")
	}
	if got := DangerLevel("command_execute"); got != "HIGH" {
		t.Fatalf("command_execute danger level = %q, want HIGH", got)
	}
	if got := MCPServerName("mcp__browser__click"); got != "browser" {
		t.Fatalf("unexpected MCP server name: %q", got)
	}
	if got := MCPServerName("not_mcp"); got != "" {
		t.Fatalf("non-mcp tool should yield empty server, got %q", got)
	}

	want := []string{"apply_patch", "command_execute", "git_commit", "write_file"}
	if got := DangerousTools(); !reflect.DeepEqual(got, want) {
		t.Fatalf("dangerous tools mismatch: got %v want %v", got, want)
	}
}
