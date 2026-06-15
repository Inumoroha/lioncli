package agent

import (
	"context"
	"fmt"
	"strings"

	"lioncli/internal/plan"
)

// Plan 进入规划-执行模式:对 goal 生成带依赖的任务 DAG,按拓扑序逐个执行
// (每任务复用 agent.Run),失败的任务阻塞其后续,全部完成后若存在失败尝试 replan。
// planner 为 nil 时返回错误;调用方(如 TUI)应据此提示"规划器未启用"。
func (a *Agent) Plan(ctx context.Context, goal string) (string, error) {
	if a.planner == nil {
		return "", fmt.Errorf("规划器未启用")
	}

	// Phase 1:生成执行计划(简单目标启发式,复杂目标调 LLM)。
	a.emit(Event{Kind: EventPlanStart, Text: goal})
	ep, err := a.planner.CreatePlan(goal)
	if err != nil {
		a.emit(Event{Kind: EventError, Err: fmt.Errorf("生成计划失败: %w", err)})
		return "", fmt.Errorf("生成计划失败: %w", err)
	}
	ep.MarkStarted()
	a.emit(Event{Kind: EventPlanReady, Text: ep.Visualize()})

	// 清空历史:每个计划独立执行,不和此前的自由聊天历史混在一起。
	a.history = nil

	// Phase 2:按拓扑序执行每个任务。
	total := len(ep.ExecutionOrder)
	var completed, failed, skipped int
	for i, taskID := range ep.ExecutionOrder {
		task := ep.Tasks[taskID]
		if task == nil {
			continue
		}
		if !task.IsExecutable(ep.Tasks) {
			task.TaskStatus = plan.TaskStatusSkipped
			skipped++
			continue
		}

		desc := fmt.Sprintf("任务 %d/%d: %s", i+1, total, task.Description)
		a.emit(Event{Kind: EventTaskStart, Text: desc})
		task.MarkStarted()

		result, runErr := a.Run(ctx, task.Description)
		if runErr != nil {
			task.MarkFailed(runErr.Error())
			failed++
			continue
		}
		task.MarkCompleted(result)
		completed++
	}

	// Phase 3:收尾,有失败则尝试 replan。
	ep.MarkCompleted()
	summary := fmt.Sprintf("计划执行完成: %d 成功 / %d 失败 / %d 跳过 · 共 %d 个任务",
		completed, failed, skipped, total)

	if ep.IsFailed() {
		failureSummary := buildFailureSummary(ep)
		summary += "\n\n" + failureSummary

		// 尝试 replan(带已完成任务 + 失败原因),失败则返回当前结果不阻塞。
		if replan, replanErr := a.planner.Replan(ep, failureSummary); replanErr == nil {
			a.emit(Event{Kind: EventPlanReady, Text: "## 重新规划(Replan)\n\n" + replan.Visualize()})
			summary += "\n\n已生成重新规划,可通过 /plan 再次执行。"
		} else {
			summary += fmt.Sprintf("\n重新规划失败: %v", replanErr)
		}
	}

	a.emit(Event{Kind: EventPlanDone, Text: summary})
	return summary, nil
}

// buildFailureSummary 收集失败任务的信息,用于 replan 或展示。
func buildFailureSummary(ep *plan.ExecutionPlan) string {
	var b strings.Builder
	b.WriteString("失败原因:\n")
	for _, taskID := range ep.ExecutionOrder {
		task := ep.Tasks[taskID]
		if task == nil || task.TaskStatus != plan.TaskStatusFailed {
			continue
		}
		fmt.Fprintf(&b, "- %s: %s\n", task.Description, task.Error)
	}
	b.WriteString("\n已完成的任务:\n")
	for _, taskID := range ep.ExecutionOrder {
		task := ep.Tasks[taskID]
		if task == nil || task.TaskStatus != plan.TaskStatusCompleted {
			continue
		}
		fmt.Fprintf(&b, "- %s: %s\n", taskID, task.Description)
	}
	return strings.TrimSpace(b.String())
}
