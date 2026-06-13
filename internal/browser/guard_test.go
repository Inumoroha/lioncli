package browser

import "testing"

func newTestGuard() *Guard {
	return NewGuard(NewSession(), NewSensitivePagePolicy(""))
}

func TestGuard_IgnoresNonChromeTools(t *testing.T) {
	g := newTestGuard()
	d := g.Check("write_file", `{"path":"x"}`)
	if d.Block != "" || d.Notice != "" {
		t.Fatalf("non-chrome tool should be untouched: %+v", d)
	}
}

func TestGuard_SensitiveWriteRequiresPerCallApproval(t *testing.T) {
	g := newTestGuard()
	// navigate 到敏感页(URL 在参数里),click 属于改写工具 → 需单步审批。
	d := g.Check("mcp__chrome-devtools__navigate_page", `{"url":"https://github.com/settings/profile"}`)
	if d.Notice != "" || d.Block != "" {
		t.Fatalf("navigate itself is not a write tool, should pass: %+v", d)
	}
	if !d.Audit.Sensitive {
		t.Fatalf("navigate target should be flagged sensitive in audit")
	}
	// 直接对敏感 URL 的 new_page? 用 click 模拟:click 不带 url,依赖 lastNavigatedURL。
	g.AfterExecution("mcp__chrome-devtools__navigate_page", `{"url":"https://github.com/settings/profile"}`, "")
	click := g.Check("mcp__chrome-devtools__click", `{"uid":"a1"}`)
	if click.Notice == "" {
		t.Fatalf("write tool on sensitive page must require per-call approval, got %+v", click)
	}
}

func TestGuard_NonSensitivePageWriteAllowed(t *testing.T) {
	g := newTestGuard()
	g.AfterExecution("mcp__chrome-devtools__navigate_page", `{"url":"https://example.com/"}`, "")
	d := g.Check("mcp__chrome-devtools__click", `{"uid":"a1"}`)
	if d.Block != "" || d.Notice != "" {
		t.Fatalf("write on non-sensitive page should pass: %+v", d)
	}
}

func TestGuard_SharedModeBlocksClosingForeignTab(t *testing.T) {
	sess := NewSession()
	sess.SwitchToShared("http://127.0.0.1:9222")
	g := NewGuard(sess, NewSensitivePagePolicy(""))

	// 未登记的标签页 → 拒绝关闭。
	d := g.Check("mcp__chrome-devtools__close_page", `{"pageId":"page-XYZ"}`)
	if d.Block == "" {
		t.Fatalf("closing a foreign tab in shared mode should be blocked")
	}

	// agent 自己开的标签页 → 允许关闭。
	sess.RecordOpenedTab("page-OWN")
	d2 := g.Check("mcp__chrome-devtools__close_page", `{"pageId":"page-OWN"}`)
	if d2.Block != "" {
		t.Fatalf("closing an agent-opened tab should be allowed: %+v", d2)
	}
}

func TestGuard_AfterExecutionRecordsOpenedTabFromResult(t *testing.T) {
	g := newTestGuard()
	g.AfterExecution("mcp__chrome-devtools__new_page", `{"url":"https://example.com"}`, "opened page-NEW123 successfully")
	if !g.session.IsAgentOpenedTab("page-NEW123") {
		t.Fatalf("new_page result page id should be recorded as agent-opened")
	}
}

func TestPolicy_GlobMatching(t *testing.T) {
	p := NewSensitivePagePolicy("")
	cases := map[string]bool{
		"https://github.com/settings/profile":          true,
		"https://www.paypal.com/myaccount":             true,
		"https://us-east-1.console.aws.amazon.com/ec2": true,
		"https://console.aws.amazon.com/ec2":           false, // 裸域无子域,*.console... 不命中(与 Java 规则一致)
		"https://example.com/":                         false,
		"https://github.com/anthropics/repo":           false,
	}
	for url, want := range cases {
		if got := p.IsSensitive(url); got != want {
			t.Errorf("IsSensitive(%q)=%v want %v", url, got, want)
		}
	}
}
