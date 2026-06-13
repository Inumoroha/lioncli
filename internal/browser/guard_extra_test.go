package browser

import "testing"

// TestGuard_CustomServerPrefix 验证 WithServerPrefix:用户在 mcp.json 改了 server 名后,
// Guard 用注入的前缀识别 chrome 工具;默认前缀则不再命中(否则会静默失守)。
func TestGuard_CustomServerPrefix(t *testing.T) {
	sess := NewSession()
	g := NewGuard(sess, NewSensitivePagePolicy(""), WithServerPrefix("mcp__chrome__"))

	// 用自定义前缀 + 敏感页改写 → 应要求单步审批。
	g.AfterExecution("mcp__chrome__navigate_page", `{"url":"https://github.com/settings/profile"}`, "")
	d := g.Check("mcp__chrome__click", `{"uid":"a1"}`)
	if d.Notice == "" {
		t.Fatalf("自定义前缀下敏感页改写应要求审批, got %+v", d)
	}

	// 默认前缀的工具在这个 Guard 看来不是 chrome 工具 → 放行(不归它管)。
	if d2 := g.Check("mcp__chrome-devtools__click", `{"uid":"a1"}`); d2.Block != "" || d2.Notice != "" {
		t.Fatalf("非本前缀工具不应被本 Guard 处理: %+v", d2)
	}
}

// TestGuard_UnknownToolDefaultsToWrite 验证白名单反转的核心安全属性:
// 未知/新增工具(不在只读白名单)在敏感页上一律按改写处理,要求审批。
func TestGuard_UnknownToolDefaultsToWrite(t *testing.T) {
	g := newTestGuard()
	g.AfterExecution("mcp__chrome-devtools__navigate_page", `{"url":"https://github.com/settings/profile"}`, "")

	// 一个我们没列进白名单的"未来工具"。
	d := g.Check("mcp__chrome-devtools__submit_form_v2", `{"uid":"a1"}`)
	if d.Notice == "" {
		t.Fatalf("敏感页上的未知工具应默认按改写要求审批, got %+v", d)
	}
}

// TestGuard_TypeTextNowGuarded 验证反转补回了旧黑名单漏掉的写工具(type_text)。
func TestGuard_TypeTextNowGuarded(t *testing.T) {
	g := newTestGuard()
	g.AfterExecution("mcp__chrome-devtools__navigate_page", `{"url":"https://www.paypal.com/myaccount"}`, "")
	if d := g.Check("mcp__chrome-devtools__type_text", `{"text":"x"}`); d.Notice == "" {
		t.Fatalf("type_text 是写操作,敏感页上应要求审批, got %+v", d)
	}
}

// TestGuard_ReadOnlyToolOnSensitivePageAllowed 验证只读工具(截图)在敏感页仍放行。
func TestGuard_ReadOnlyToolOnSensitivePageAllowed(t *testing.T) {
	g := newTestGuard()
	g.AfterExecution("mcp__chrome-devtools__navigate_page", `{"url":"https://github.com/settings/profile"}`, "")
	if d := g.Check("mcp__chrome-devtools__take_screenshot", `{}`); d.Notice != "" || d.Block != "" {
		t.Fatalf("只读工具在敏感页应放行, got %+v", d)
	}
}

// recordingSink 收集审计记录,供断言。
type recordingSink struct {
	records []struct {
		tool string
		meta AuditMetadata
	}
}

func (s *recordingSink) RecordBrowserAudit(toolName string, meta AuditMetadata) {
	s.records = append(s.records, struct {
		tool string
		meta AuditMetadata
	}{toolName, meta})
}

// TestGuard_AuditSinkReceivesRecords 验证 WithAuditSink:每次 chrome 工具检查都落地审计,
// 且非 chrome 工具不上报。
func TestGuard_AuditSinkReceivesRecords(t *testing.T) {
	sink := &recordingSink{}
	g := NewGuard(NewSession(), NewSensitivePagePolicy(""), WithAuditSink(sink))

	g.Check("mcp__chrome-devtools__navigate_page", `{"url":"https://github.com/settings/profile"}`)
	if len(sink.records) != 1 {
		t.Fatalf("应上报 1 条审计, got %d", len(sink.records))
	}
	if !sink.records[0].meta.Sensitive || sink.records[0].meta.TargetURL == "" {
		t.Fatalf("审计应标记敏感并带目标 URL: %+v", sink.records[0].meta)
	}

	// 非 chrome 工具不应触发审计。
	g.Check("write_file", `{"path":"x"}`)
	if len(sink.records) != 1 {
		t.Fatalf("非 chrome 工具不应上报审计, got %d", len(sink.records))
	}
}
