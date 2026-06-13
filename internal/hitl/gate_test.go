package hitl

import (
	"io"
	"testing"
)

// stubGuard 是 ToolGuard 的测试桩。
type stubGuard struct {
	block      string
	notice     string
	afterCalls int
}

func (g *stubGuard) Inspect(toolName, argsJSON string) (string, string) { return g.block, g.notice }
func (g *stubGuard) AfterExecution(toolName, argsJSON, result string)   { g.afterCalls++ }

// stubRenderer 记录 PromptApproval 的调用,供 RendererHitlHandler 使用。
type stubRenderer struct {
	calls  int
	last   ApprovalRequest
	result ApprovalResult
}

func (r *stubRenderer) PromptApproval(req ApprovalRequest) ApprovalResult {
	r.calls++
	r.last = req
	return r.result
}
func (r *stubRenderer) Stream() io.Writer { return io.Discard }

func TestApplyGate_GuardBlockRefusesWithoutApproval(t *testing.T) {
	r := &stubRenderer{result: ApproveAll()}
	h := NewRendererHitlHandler(r, true)
	g := &stubGuard{block: "禁止关闭用户标签页"}

	_, denyMsg, blocked := ApplyGate(h, g, "mcp__chrome-devtools__close_page", `{"pageId":"page-x"}`)
	if !blocked {
		t.Fatal("guard block should block execution")
	}
	if r.calls != 0 {
		t.Fatalf("block must not trigger approval prompt, got %d calls", r.calls)
	}
	if denyMsg == "" {
		t.Fatal("expected a deny message")
	}
}

func TestApplyGate_SensitiveOverridesApproveAll(t *testing.T) {
	r := &stubRenderer{result: ApproveAll()}
	h := NewRendererHitlHandler(r, true)
	tool := "mcp__chrome-devtools__click"

	// 第一次:无敏感标记,用户选择"全部放行该工具" → 记录 approve-all。
	if _, _, blocked := ApplyGate(h, &stubGuard{}, tool, `{}`); blocked {
		t.Fatal("first call should be approved")
	}
	if r.calls != 1 {
		t.Fatalf("first call should prompt once, got %d", r.calls)
	}

	// 第二次:同工具但 guard 给出敏感标记 → 必须再次单步审批,不能复用 approve-all。
	g := &stubGuard{notice: "敏感页面命中"}
	if _, _, blocked := ApplyGate(h, g, tool, `{}`); blocked {
		t.Fatal("sensitive call approved by renderer should not block")
	}
	if r.calls != 2 {
		t.Fatalf("sensitive op must re-prompt despite approve-all, got %d calls", r.calls)
	}
	if r.last.SensitiveNotice != "敏感页面命中" {
		t.Fatalf("sensitive notice not propagated to approval request: %q", r.last.SensitiveNotice)
	}

	// 第三次:同工具、无敏感标记 → approve-all 短路,不再弹审批。
	if _, _, _ = ApplyGate(h, &stubGuard{}, tool, `{}`); r.calls != 2 {
		t.Fatalf("non-sensitive repeat should reuse approve-all (no new prompt), got %d calls", r.calls)
	}
}

func TestApplyGate_NilGuardUnaffected(t *testing.T) {
	r := &stubRenderer{result: Approve()}
	h := NewRendererHitlHandler(r, true)
	// 非审批清单工具,无 guard → 直接放行,不弹审批。
	if _, _, blocked := ApplyGate(h, nil, "read_file", `{"path":"x"}`); blocked {
		t.Fatal("read_file should pass freely")
	}
	if r.calls != 0 {
		t.Fatalf("safe tool must not prompt, got %d", r.calls)
	}
}
