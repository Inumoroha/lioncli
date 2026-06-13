package browser

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// defaultServerPrefix 是 Chrome DevTools MCP server 的**默认**工具前缀。
// 真实前缀由 mcp 层按 server 名动态生成(mcp__<server>__),用户在 mcp.json 里
// 改了 server 名这里就对不上,故宿主应通过 WithServerPrefix 注入实际前缀;
// 这个常量只是最常见配置下的兜底默认。
const defaultServerPrefix = "mcp__chrome-devtools__"

// readOnlyTools 是**只读/无副作用**的浏览器工具白名单(去掉 server 前缀后的本地名)。
// 安全取向:白名单之外的工具(含 server 升级后新增的、我们还不认识的)一律按
// "改写操作"对待——宁可对敏感页多要一次审批,也不放过未知的写操作。
// 这比旧的 writeTools 黑名单更安全:黑名单漏列(旧版就漏了 type_text/emulate)
// 会让新写操作在敏感页静默放行。
//
// 说明:navigate_page / new_page / select_page / close_page 视为导航/生命周期
// 操作而非"页面内容改写",保留为只读(到达敏感页本身不需要逐次审批,真正危险的是
// 在敏感页上的点击/填表/执行脚本)。close_page 另有独立的 shared 模式拦截规则。
var readOnlyTools = map[string]struct{}{
	"navigate_page": {}, "new_page": {}, "select_page": {}, "close_page": {},
	"list_pages": {}, "list_console_messages": {}, "list_network_requests": {},
	"get_console_message": {}, "get_network_request": {},
	"take_screenshot": {}, "take_snapshot": {}, "take_heapsnapshot": {},
	"wait_for":                {},
	"performance_start_trace": {}, "performance_stop_trace": {}, "performance_analyze_insight": {},
	"lighthouse_audit": {},
}

var pageIDPattern = regexp.MustCompile(`(page[-_][A-Za-z0-9_-]+)`)

// Decision 是 Guard 对一次浏览器工具调用的判定。
//   - Block 非空:直接拒绝执行(硬安全规则,与 HITL 开关无关);
//   - Notice 非空:命中敏感页面的改写操作,必须单步审批,不能复用"全部放行";
//   - 二者皆空:放行(普通 MCP 审批逻辑照常)。
type Decision struct {
	Block  string
	Notice string
	Audit  AuditMetadata
}

// Guard 在浏览器工具落地前做安全检查,移植自 Java 版 BrowserGuard。
// 它只关心 Chrome DevTools MCP 工具,对其余工具一律放行。
type Guard struct {
	session      *Session
	policy       *SensitivePagePolicy
	serverPrefix string
	auditSink    AuditSink
}

// Option 配置 Guard 的可选行为。
type Option func(*Guard)

// WithServerPrefix 用实际的 MCP 工具前缀覆盖默认值。
// 宿主应传入 mcp 层为 chrome-devtools server 生成的前缀(mcp__<server>__),
// 避免用户改了 server 名后 Guard 因前缀对不上而静默失效。空串则忽略。
func WithServerPrefix(prefix string) Option {
	return func(g *Guard) {
		if strings.TrimSpace(prefix) != "" {
			g.serverPrefix = prefix
		}
	}
}

// WithAuditSink 注入审计落地端;每次对 chrome 工具的检查都会上报一条审计。
func WithAuditSink(sink AuditSink) Option {
	return func(g *Guard) { g.auditSink = sink }
}

// NewGuard 构造一个 Guard。session/policy 不能为 nil。
func NewGuard(session *Session, policy *SensitivePagePolicy, opts ...Option) *Guard {
	g := &Guard{session: session, policy: policy, serverPrefix: defaultServerPrefix}
	for _, opt := range opts {
		opt(g)
	}
	return g
}

// isWriteTool 报告某本地工具名是否应按"改写操作"对待:不在只读白名单里即为写。
func isWriteTool(local string) bool {
	_, readOnly := readOnlyTools[local]
	return !readOnly
}

// Check 对一次工具调用做完整判定(返回 Decision,含审计信息)。
func (g *Guard) Check(toolName, argsJSON string) Decision {
	if !g.isChromeTool(toolName) {
		return Decision{}
	}
	local := g.localToolName(toolName)
	args := parseArgs(argsJSON)

	target := targetURL(local, args)
	effective := target
	if effective == "" {
		effective = g.session.LastNavigatedURL()
	}
	matched, pattern := g.policy.Match(effective)
	audit := AuditMetadata{BrowserMode: g.session.Mode().String(), Sensitive: matched, TargetURL: effective}

	// 审计落地:每次对 chrome 工具的检查都上报一条(若宿主注入了 sink)。
	if g.auditSink != nil {
		g.auditSink.RecordBrowserAudit(toolName, audit)
	}

	// SHARED 模式下,拒绝关闭不是 agent 自己开的标签页,避免误关用户的页面。
	if local == "close_page" && g.session.Mode() == ModeShared && !g.session.IsAgentOpenedTab(pageID(args)) {
		return Decision{
			Block: "shared 浏览器模式下拒绝关闭非 teacli 创建的标签页,请手动关闭该 Chrome 标签页",
			Audit: audit,
		}
	}

	// 敏感页面上的改写操作:强制单步审批。
	if matched && isWriteTool(local) {
		return Decision{
			Notice: "敏感页面命中规则 " + pattern + ",本次浏览器改写操作必须单步审批,不能复用全部放行。",
			Audit:  audit,
		}
	}
	return Decision{Audit: audit}
}

// Inspect 实现 hitl.ToolGuard:返回 (block, sensitiveNotice)。
func (g *Guard) Inspect(toolName, argsJSON string) (block, sensitiveNotice string) {
	d := g.Check(toolName, argsJSON)
	return d.Block, d.Notice
}

// AfterExecution 实现 hitl.ToolGuard:工具执行后更新会话状态
// (记住导航 URL、记录 agent 新开的标签页)。
func (g *Guard) AfterExecution(toolName, argsJSON, result string) {
	if !g.isChromeTool(toolName) {
		return
	}
	local := g.localToolName(toolName)
	args := parseArgs(argsJSON)

	if local == "navigate_page" || local == "new_page" {
		if u := targetURL(local, args); u != "" {
			g.session.RememberNavigation(u)
		}
	}
	if local == "new_page" {
		pid := pageID(args)
		if pid == "" {
			pid = extractPageID(result)
		}
		g.session.RecordOpenedTab(pid)
	}
}

func (g *Guard) isChromeTool(toolName string) bool {
	return strings.HasPrefix(toolName, g.serverPrefix)
}

func (g *Guard) localToolName(toolName string) string {
	return strings.TrimPrefix(toolName, g.serverPrefix)
}

func parseArgs(argsJSON string) map[string]any {
	if strings.TrimSpace(argsJSON) == "" {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &m); err != nil || m == nil {
		return map[string]any{}
	}
	return m
}

// targetURL 仅 navigate_page / new_page 带显式 url。
func targetURL(local string, args map[string]any) string {
	if local != "navigate_page" && local != "new_page" {
		return ""
	}
	return strings.TrimSpace(asText(args["url"]))
}

// pageID 依次尝试 pageIdx / pageId / uid。
func pageID(args map[string]any) string {
	for _, key := range []string{"pageIdx", "pageId", "uid"} {
		if v := strings.TrimSpace(asText(args[key])); v != "" {
			return v
		}
	}
	return ""
}

func extractPageID(result string) string {
	if result == "" {
		return ""
	}
	return pageIDPattern.FindString(result)
}

// asText 把 JSON 解析出的任意值转成字符串(数字去掉多余小数)。
func asText(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		return fmt.Sprint(t)
	}
}
