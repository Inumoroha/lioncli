package multiagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"lioncli/internal/hitl"
	"lioncli/internal/llm"
	"lioncli/internal/tool"
)

// Orchestrator 是 Multi-Agent 系统的"主控":管理规划者/执行者/检查者,
// 串起 拆解→执行→审查→汇总 的协作闭环。
//
// 采用主从架构,主控负责路由与汇总,子代理负责具体认知/执行。
// 协作流程:
//  1. 用户目标 → 规划者拆成有序子任务(JSON 计划);
//  2. 按依赖拓扑序遍历子任务;
//  3. 每个子任务交执行者(带工具)落地;
//  4. 检查者评判执行结果,驳回则带反馈让执行者重做(有限次重试);
//  5. 全部完成后,规划者角色做一次最终汇总,产出面向用户的答复。
//
// 当前实现按拓扑序串行执行(HITL 审批本就串行化,且 LLM 调用为主要耗时)。
// 计划里的依赖批次已算出,后续若要并行同批,只需在 runSteps 内并发分发即可。
type Orchestrator struct {
	client   llm.Client
	model    string
	registry *tool.ToolRegistry
	gate     hitl.HitlHandler
	guard    hitl.ToolGuard
	observer Observer

	// maxReviewRetries 是检查者驳回后,执行者带反馈重做的最大次数。
	maxReviewRetries int
}

func NewOrchestrator(client llm.Client, model string, registry *tool.ToolRegistry, gate hitl.HitlHandler, guard hitl.ToolGuard) *Orchestrator {
	return &Orchestrator{
		client:           client,
		model:            model,
		registry:         registry,
		gate:             gate,
		guard:            guard,
		maxReviewRetries: 1,
	}
}

// SetObserver 注入进度观察者(由 agent.RunTeam 桥接到 TUI)。
func (o *Orchestrator) SetObserver(obs Observer) { o.observer = obs }

func (o *Orchestrator) emit(e Event) {
	if o.observer != nil {
		o.observer(e)
	}
}

// newSub 构造一个挂好观察者的子代理。
func (o *Orchestrator) newSub(name string, role AgentRole, useTools bool) *SubAgent {
	s := NewSubAgent(name, role, o.client, o.model, o.registry, o.gate, o.guard, useTools)
	s.setObserver(o.observer)
	return s
}

// Run 执行完整的多智能体协作,返回面向用户的最终答复。
func (o *Orchestrator) Run(ctx context.Context, goal string) (string, error) {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return "", fmt.Errorf("目标为空")
	}

	// 1) 规划。
	o.emit(Event{Kind: EventPlanStart, Role: AgentRolePlanner, Text: goal})
	planner := o.newSub("planner", AgentRolePlanner, false)
	planRaw, err := planner.Run(ctx, plannerPrompt(goal))
	if err != nil {
		o.emit(Event{Kind: EventError, Role: AgentRolePlanner, Text: err.Error()})
		return "", fmt.Errorf("规划者失败: %w", err)
	}
	steps := topoOrder(parsePlan(planRaw, goal))
	o.emit(Event{Kind: EventPlanReady, Role: AgentRolePlanner, Text: renderPlan(steps)})

	// 2) 逐步执行 + 审查。
	results := make([]stepResult, 0, len(steps))
	for idx, step := range steps {
		o.emit(Event{
			Kind: EventStepStart,
			Role: AgentRoleWorker,
			Text: fmt.Sprintf("步骤 %d/%d: %s", idx+1, len(steps), step.Description),
		})

		final, err := o.runStep(ctx, goal, step, results)
		if err != nil {
			o.emit(Event{Kind: EventError, Role: AgentRoleWorker, Text: err.Error()})
			return "", err
		}
		results = append(results, stepResult{Step: step, Output: final})
	}

	// 3) 汇总。
	o.emit(Event{Kind: EventInfo, Role: AgentRolePlanner, Text: "正在汇总各步骤结果…"})
	// 汇总不走规划者人格(其 system prompt 强制输出 JSON 计划),用独立的合成 system prompt,
	// 否则最终答复会变成一份计划 JSON 而非面向用户的回答。
	summarizer := o.newSub("summarizer", AgentRolePlanner, false).withSystem(synthesisSystemPrompt)
	summary, err := summarizer.Run(ctx, synthesisPrompt(goal, results))
	if err != nil {
		// 汇总失败不致命:退化为把各步骤结果拼起来返回。
		summary = fallbackSummary(results)
	}

	o.emit(Event{Kind: EventDone, Role: AgentRolePlanner, Text: summary})
	return summary, nil
}

// runStep 执行单个子任务,并经检查者审查;被驳回时带反馈重做,直到通过或用尽重试。
func (o *Orchestrator) runStep(ctx context.Context, goal string, step Step, prior []stepResult) (string, error) {
	worker := o.newSub(fmt.Sprintf("worker-%d", step.ID), AgentRoleWorker, true)
	reviewer := o.newSub(fmt.Sprintf("reviewer-%d", step.ID), AgentRoleReviewer, false)

	var feedback string
	for attempt := 0; attempt <= o.maxReviewRetries; attempt++ {
		output, err := worker.Run(ctx, workerPrompt(goal, step, prior, feedback))
		if err != nil {
			return "", fmt.Errorf("执行者(步骤 %d)失败: %w", step.ID, err)
		}
		o.emit(Event{Kind: EventStepResult, Role: AgentRoleWorker, Text: output})

		verdict := reviewer.reviewStep(ctx, step, output)
		if verdict.Approved {
			o.emit(Event{Kind: EventReview, Role: AgentRoleReviewer, Text: "✅ 通过: " + step.Description})
			return output, nil
		}

		feedback = verdict.Feedback
		o.emit(Event{Kind: EventReview, Role: AgentRoleReviewer, Text: "🔁 驳回: " + feedback})
		if attempt == o.maxReviewRetries {
			// 用尽重试仍未通过:接受最后一版结果,但在结果上标注未通过审查,交由汇总环节权衡。
			return output + "\n\n[注:该步骤经检查者驳回,反馈: " + feedback + "]", nil
		}
	}
	return "", nil // 不可达
}

// stepResult 记录一个步骤的最终产出。
type stepResult struct {
	Step   Step
	Output string
}

// reviewVerdict 是检查者的结构化结论。
type reviewVerdict struct {
	Approved bool
	Feedback string
}

// reviewStep 让检查者评判一个步骤结果,解析其 JSON 裁决。解析失败时保守地视为通过,
// 避免因检查者偶发的非结构化输出把整个流程卡在重试里。
func (s *SubAgent) reviewStep(ctx context.Context, step Step, output string) reviewVerdict {
	raw, err := s.Run(ctx, reviewerPrompt(step, output))
	if err != nil {
		return reviewVerdict{Approved: true}
	}
	var parsed struct {
		Verdict  string `json:"verdict"`
		Feedback string `json:"feedback"`
	}
	jsonPart := raw
	if obj := extractJSONObject(raw); obj != "" {
		jsonPart = obj
	}
	if err := json.Unmarshal([]byte(jsonPart), &parsed); err != nil {
		return reviewVerdict{Approved: true}
	}
	if strings.EqualFold(strings.TrimSpace(parsed.Verdict), "REJECTED") {
		fb := strings.TrimSpace(parsed.Feedback)
		if fb == "" {
			fb = "检查者驳回但未给出具体反馈"
		}
		return reviewVerdict{Approved: false, Feedback: fb}
	}
	return reviewVerdict{Approved: true}
}

// ---- prompt 构造 ----

// synthesisSystemPrompt 用于最终汇总:中立的"整合者"人格,直接产出面向用户的答复,
// 不带规划者那种"只输出 JSON 计划"的约束。
const synthesisSystemPrompt = `你是多智能体团队的汇总者。
你的职责:把各子任务的执行结果整合成一份连贯、完整、面向用户的最终答复。
要求:
- 直接给出最终答案,不要输出 JSON、不要复述提示词、不要罗列"步骤几"。
- 语言简洁准确,结构清晰(可用小标题/列表),只保留对用户有价值的内容。`

func plannerPrompt(goal string) string {
	return "用户目标:\n" + goal + "\n\n请据此输出 JSON 格式的执行计划。"
}

func workerPrompt(goal string, step Step, prior []stepResult, feedback string) string {
	var b strings.Builder
	b.WriteString("总目标:\n")
	b.WriteString(goal)
	b.WriteString("\n\n你负责的子任务:\n")
	b.WriteString(step.Description)
	if ctx := priorContext(step, prior); ctx != "" {
		b.WriteString("\n\n已完成的前置步骤结果(供参考):\n")
		b.WriteString(ctx)
	}
	if strings.TrimSpace(feedback) != "" {
		b.WriteString("\n\n检查者对你上一版结果的反馈,请据此改进:\n")
		b.WriteString(feedback)
	}
	b.WriteString("\n\n请完成该子任务,必要时调用工具,并在最后简洁总结你的产出。")
	return b.String()
}

// priorContext 汇集本步骤依赖的前置步骤结果;无显式依赖时给出全部已完成步骤,
// 让执行者始终能看到上下文。
func priorContext(step Step, prior []stepResult) string {
	want := make(map[int]struct{}, len(step.Dependencies))
	for _, d := range step.Dependencies {
		want[d] = struct{}{}
	}
	var b strings.Builder
	for _, r := range prior {
		if len(want) > 0 {
			if _, ok := want[r.Step.ID]; !ok {
				continue
			}
		}
		fmt.Fprintf(&b, "- 步骤 %d(%s)的结果: %s\n", r.Step.ID, r.Step.Description, truncate(r.Output, 600))
	}
	return strings.TrimRight(b.String(), "\n")
}

func reviewerPrompt(step Step, output string) string {
	return fmt.Sprintf("子任务:\n%s\n\n执行者交付的结果:\n%s\n\n请评判并按要求输出 JSON 裁决。", step.Description, output)
}

func synthesisPrompt(goal string, results []stepResult) string {
	var b strings.Builder
	b.WriteString("总目标:\n")
	b.WriteString(goal)
	b.WriteString("\n\n各子任务的执行结果如下:\n")
	for _, r := range results {
		fmt.Fprintf(&b, "\n## 步骤 %d: %s\n%s\n", r.Step.ID, r.Step.Description, r.Output)
	}
	b.WriteString("\n请基于以上结果,面向用户给出连贯、完整的最终答复(直接给结论,不要复述本提示)。")
	return b.String()
}

func fallbackSummary(results []stepResult) string {
	var b strings.Builder
	b.WriteString("各步骤结果汇总:\n")
	for _, r := range results {
		fmt.Fprintf(&b, "\n## 步骤 %d: %s\n%s\n", r.Step.ID, r.Step.Description, r.Output)
	}
	return strings.TrimRight(b.String(), "\n")
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + " …[截断]"
}
