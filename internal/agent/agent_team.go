package agent

import (
	"context"
	"fmt"

	"lioncli/internal/multiagent"
)

// RunTeam 进入多智能体协作模式:把 goal 交给 multiagent.Orchestrator,由
// 规划者→执行者→检查者 协同完成,过程事件实时桥接为 agent.Event 推给 TUI。
//
// 它与单体 Run 相互独立:复用同一个工具 Registry 与 HITL 闸门(执行者的工具调用
// 同样要过审批),但用各自全新的对话历史,不污染主聊天上下文。
func (a *Agent) RunTeam(ctx context.Context, goal string) (string, error) {
	orch := multiagent.NewOrchestrator(a.llmClient, a.model, a.registry, a.gate, a.browserGuard)
	orch.SetObserver(a.bridgeTeamEvent)

	summary, err := orch.Run(ctx, goal)
	if err != nil {
		a.emit(Event{Kind: EventError, Err: err})
		return "", err
	}

	// 把最终汇总作为一条 assistant 文本记进短期记忆,使后续自由聊天能延续上下文。
	if a.memMgr != nil {
		a.memMgr.AddUserMessage("[team] " + goal)
		a.memMgr.AddAssistantMessage(summary)
	}
	return summary, nil
}

// bridgeTeamEvent 把 multiagent.Event 翻译成 agent.Event。
// 工具事件保留 ToolID/Name,复用 TUI 已有的工具块渲染;其余角色动态走 EventInfo/文本。
func (a *Agent) bridgeTeamEvent(e multiagent.Event) {
	switch e.Kind {
	case multiagent.EventToolStart:
		a.emit(Event{
			Kind:      EventToolStart,
			ToolID:    e.ToolID,
			ToolName:  e.ToolName,
			ToolInput: e.ToolInput,
		})
	case multiagent.EventToolEnd:
		a.emit(Event{
			Kind:       EventToolEnd,
			ToolID:     e.ToolID,
			ToolName:   e.ToolName,
			ToolOutput: e.Text,
			IsError:    e.IsError,
		})
	case multiagent.EventError:
		a.emit(Event{Kind: EventError, Err: fmt.Errorf("%s", e.Text)})
	case multiagent.EventDone:
		// 最终汇总作为 assistant 文本展示(走 markdown 渲染)。
		a.emit(Event{Kind: EventAssistantText, Text: e.Text})
	default:
		// 规划/步骤/审查/旁白等统一作为信息行展示,带角色前缀。
		a.emit(Event{Kind: EventInfo, Text: teamEventPrefix(e) + e.Text})
	}
}

// teamEventPrefix 给信息行加一个角色/阶段标识前缀。
func teamEventPrefix(e multiagent.Event) string {
	switch e.Kind {
	case multiagent.EventPlanStart:
		return "🧭 规划者 · "
	case multiagent.EventPlanReady:
		return ""
	case multiagent.EventStepStart:
		return "🔧 执行者 · "
	case multiagent.EventStepResult:
		return "📤 执行者结果 · "
	case multiagent.EventReview:
		return "🔍 检查者 · "
	default:
		return ""
	}
}
