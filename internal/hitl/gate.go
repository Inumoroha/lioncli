package hitl

import "strings"

// ToolGuard 是工具落地前的领域安全检查钩子(如浏览器操作护栏),可选。
// 它独立于 HITL 审批:即便审批关闭,Block 这类硬安全规则仍然生效。
//
//   - Inspect:审批前检查。block 非空 → 直接拒绝(不弹审批);
//     sensitiveNotice 非空 → 命中敏感操作,强制单步审批、不复用"全部放行"。
//   - AfterExecution:工具执行后回调,供 guard 更新自身会话状态。
//
// 由 internal/browser.Guard 实现;hitl 只依赖该接口,不反向依赖具体实现。
type ToolGuard interface {
	Inspect(toolName, argsJSON string) (block, sensitiveNotice string)
	AfterExecution(toolName, argsJSON, result string)
}

// ApplyGate 是工具执行前 HITL 审批的统一入口,供 agent 与 multiagent 子代理共用,
// 保证两条执行路径的审批语义完全一致(危险工具/MCP 工具才拦,approve-all 短路等)。
//
// guard 可为 nil。非 nil 时先于审批运行:
//   - guard 判定 block → 直接拒绝,不进入审批;
//   - guard 给出 sensitiveNotice → 通过 ApprovalRequest 强制单步审批(handler 内部据此
//     跳过 approve-all 短路)。
//
// 入参 argsJSON 是工具参数的 JSON 文本(用于审批卡片展示与"修改参数"回填)。
// 返回:
//   - effectiveJSON:放行时实际应使用的参数 JSON(用户改过则为新值,否则原样);
//   - denyMsg:被拒/跳过/被 guard 拦截时回给 LLM 的说明文本;
//   - blocked:true 表示不得执行该工具。
func ApplyGate(handler HitlHandler, guard ToolGuard, toolName, argsJSON string) (effectiveJSON, denyMsg string, blocked bool) {
	return ApplyGateWithContext(handler, guard, toolName, argsJSON, "", "")
}

func ApplyGateWithContext(handler HitlHandler, guard ToolGuard, toolName, argsJSON, suggestion, callerContext string) (effectiveJSON, denyMsg string, blocked bool) {
	sensitiveNotice := ""
	if guard != nil {
		block, notice := guard.Inspect(toolName, argsJSON)
		if block != "" {
			// 硬安全规则:无条件拒绝,与 HITL 开关无关。
			return argsJSON, "[HITL] 浏览器操作被拒绝:" + block, true
		}
		sensitiveNotice = notice
	}

	if handler == nil || !handler.IsEnabled() || !RequiresApproval(toolName) {
		return argsJSON, "", false
	}

	res := handler.RequestApproval(NewApprovalRequest(toolName, argsJSON, suggestion, callerContext, sensitiveNotice))
	switch {
	case res.IsRejected():
		reason := strings.TrimSpace(res.Reason)
		if reason == "" {
			reason = "用户拒绝执行此操作"
		}
		return argsJSON, "[HITL] 操作被拒绝:" + reason, true
	case res.IsSkipped():
		return argsJSON, "[HITL] 操作被用户跳过", true
	default:
		// 批准(含 approve-all / modified)。Modified 时取用户改后的参数。
		return res.EffectiveArguments(argsJSON), "", false
	}
}
