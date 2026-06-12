package plan

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const defaultModel = "deepseek-chat"

const plannerPrompt = `You are a task planner. Split the user goal into an executable JSON plan.

Output requirements:
1. Return JSON only. Do not return explanations, Markdown, or comments.
2. JSON shape:
{
  "summary": "one sentence summary",
  "tasks": [
    {
      "id": "step_1",
      "description": "task description",
      "type": "ANALYSIS",
      "tool_name": "",
      "args": {},
      "dependencies": []
    }
  ]
}
3. type must be one of: PLANNING, FILE_READ, FILE_WRITE, COMMAND, ANALYSIS, VERIFICATION.
4. If a task needs real execution, fill tool_name and args. Local tools include read_file, write_file, command_execute.
5. dependencies must contain task ids from this same JSON plan.
6. Keep the task list concise and make sure dependencies are acyclic.`

type Planner struct {
	chatClient ChatClient
	Plan       *ExecutionPlan
}

func NewPlanner(chatClient ChatClient, plan *ExecutionPlan) *Planner {
	return &Planner{
		chatClient: chatClient,
		Plan:       plan,
	}
}

func (p *Planner) CreatePlan(goal string) (*ExecutionPlan, error) {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return nil, fmt.Errorf("goal cannot be empty")
	}

	if isSimpleGoal(goal) {
		plan := createMinimalPlan(goal)
		p.Plan = plan
		return plan, nil
	}

	if p.chatClient == nil {
		return nil, fmt.Errorf("planner: chat client is nil")
	}

	responseText, err := p.chatClient.Chat(
		plannerPrompt,
		"Create an execution plan for this task:\n"+goal,
	)
	if err != nil {
		return nil, fmt.Errorf("planner: LLM call failed: %w", err)
	}

	plan, err := p.parsePlan(goal, responseText)
	if err != nil {
		return nil, err
	}

	p.Plan = plan
	return plan, nil
}

func (p *Planner) parsePlan(goal, planJSON string) (*ExecutionPlan, error) {
	cleaned := cleanPlannerJSON(planJSON)

	var raw struct {
		Summary string `json:"summary"`
		Tasks   []struct {
			ID           string         `json:"id"`
			Description  string         `json:"description"`
			Type         string         `json:"type"`
			ToolName     string         `json:"tool_name"`
			Args         map[string]any `json:"args"`
			Dependencies []string       `json:"dependencies"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(cleaned), &raw); err != nil {
		return nil, fmt.Errorf("failed to parse planner JSON: %w", err)
	}
	if len(raw.Tasks) == 0 {
		return nil, fmt.Errorf("planner returned no tasks")
	}

	plan := NewExecutionPlan(generatePlanID(), goal)
	plan.Summary = strings.TrimSpace(raw.Summary)

	idMapping := make(map[string]string, len(raw.Tasks))
	for i, taskNode := range raw.Tasks {
		originalID := strings.TrimSpace(taskNode.ID)
		if originalID == "" {
			return nil, fmt.Errorf("planner task at index %d has empty id", i)
		}
		if _, exists := idMapping[originalID]; exists {
			return nil, fmt.Errorf("planner returned duplicate task id %q", originalID)
		}

		description := strings.TrimSpace(taskNode.Description)
		if description == "" {
			return nil, fmt.Errorf("planner task %q has empty description", originalID)
		}

		taskType, err := parseTaskTypeStrict(taskNode.Type)
		if err != nil {
			return nil, fmt.Errorf("planner task %q: %w", originalID, err)
		}

		taskID := fmt.Sprintf("task_%d", i+1)
		idMapping[originalID] = taskID
		plan.Tasks[taskID] = &Task{
			ID:          taskID,
			Description: description,
			TaskType:    taskType,
			TaskStatus:  TaskStatusPending,
			ToolName:    strings.TrimSpace(taskNode.ToolName),
			Args:        cloneArgs(taskNode.Args),
			DependsOn:   make([]string, 0),
			DependedBy:  make([]string, 0),
		}
	}

	for i, taskNode := range raw.Tasks {
		taskID := fmt.Sprintf("task_%d", i+1)
		task := plan.Tasks[taskID]
		originalID := strings.TrimSpace(taskNode.ID)
		seenDeps := make(map[string]struct{}, len(taskNode.Dependencies))

		for _, rawDepID := range taskNode.Dependencies {
			originalDepID := strings.TrimSpace(rawDepID)
			if originalDepID == "" {
				return nil, fmt.Errorf("planner task %q has empty dependency id", originalID)
			}
			if originalDepID == originalID {
				return nil, fmt.Errorf("planner task %q cannot depend on itself", originalID)
			}
			if _, exists := seenDeps[originalDepID]; exists {
				return nil, fmt.Errorf("planner task %q has duplicate dependency %q", originalID, originalDepID)
			}
			seenDeps[originalDepID] = struct{}{}

			depID, ok := idMapping[originalDepID]
			if !ok {
				return nil, fmt.Errorf("planner task %q depends on unknown task %q", originalID, originalDepID)
			}

			task.DependsOn = append(task.DependsOn, depID)
			plan.Tasks[depID].DependedBy = append(plan.Tasks[depID].DependedBy, taskID)
		}
	}

	if err := plan.ComputeExecutionOrder(); err != nil {
		return nil, fmt.Errorf("invalid execution plan: %w", err)
	}
	if plan.Summary == "" {
		plan.Summary = buildMinimalSummary(goal)
	}
	return plan, nil
}

func (p *Planner) Replan(failedPlan *ExecutionPlan, failureReason string) (*ExecutionPlan, error) {
	if failedPlan == nil {
		return nil, fmt.Errorf("failedPlan cannot be nil")
	}

	goal := strings.TrimSpace(failedPlan.Goal)
	if goal == "" {
		return nil, fmt.Errorf("failedPlan goal cannot be empty")
	}

	failureReason = strings.TrimSpace(failureReason)
	if failureReason == "" {
		failureReason = "unknown execution failure"
	}

	var context strings.Builder
	context.WriteString("Original task: ")
	context.WriteString(goal)
	context.WriteString("\nFailure reason: ")
	context.WriteString(failureReason)
	context.WriteString("\nCompleted tasks:\n")

	completedCount := 0
	for _, taskID := range failedPlan.ExecutionOrder {
		task := failedPlan.Tasks[taskID]
		if task == nil || task.TaskStatus != TaskStatusCompleted {
			continue
		}
		completedCount++
		context.WriteString("- ")
		context.WriteString(task.ID)
		context.WriteString(": ")
		context.WriteString(task.Description)
		context.WriteString("\n")
	}
	if completedCount == 0 {
		context.WriteString("- none\n")
	}

	context.WriteString("\nCreate a new execution plan that reuses completed results when useful and avoids the previous failure.")
	return p.CreatePlan(context.String())
}

func cleanPlannerJSON(planJSON string) string {
	cleaned := strings.TrimSpace(planJSON)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```JSON")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	return strings.TrimSpace(cleaned)
}

func parseTaskType(typeStr string) TaskType {
	taskType, err := parseTaskTypeStrict(typeStr)
	if err != nil {
		return TaskTypeAnalysis
	}
	return taskType
}

func parseTaskTypeStrict(typeStr string) (TaskType, error) {
	normalized := strings.ToUpper(strings.TrimSpace(typeStr))
	switch normalized {
	case "PLANNING":
		return TaskTypePlanning, nil
	case "FILE_READ", "FILE_READING":
		return TaskTypeFileRead, nil
	case "FILE_WRITE", "FILE_WRITING":
		return TaskTypeFileWrite, nil
	case "COMMAND":
		return TaskTypeCommand, nil
	case "ANALYSIS":
		return TaskTypeAnalysis, nil
	case "VERIFICATION":
		return TaskTypeVerification, nil
	default:
		if normalized == "" {
			return "", fmt.Errorf("task type is empty")
		}
		return "", fmt.Errorf("unknown task type %q", typeStr)
	}
}

func generatePlanID() string {
	return fmt.Sprintf("plan_%d", time.Now().UnixMilli())
}

func isSimpleGoal(goal string) bool {
	if goal == "" {
		return false
	}

	hasMultiStepCue := strings.Contains(goal, "然后") ||
		strings.Contains(goal, "并且") ||
		strings.Contains(goal, "并") ||
		strings.Contains(goal, "最后") ||
		strings.Contains(goal, "同时") ||
		strings.Contains(goal, "之后") ||
		strings.Contains(goal, "接着") ||
		strings.Contains(goal, "以及")
	if hasMultiStepCue {
		return false
	}

	if len([]rune(goal)) > 30 {
		return false
	}

	return strings.Contains(goal, "列出") ||
		strings.Contains(goal, "查看") ||
		strings.Contains(goal, "读取") ||
		strings.Contains(goal, "显示") ||
		strings.Contains(goal, "执行") ||
		strings.Contains(goal, "运行") ||
		strings.Contains(goal, "搜索") ||
		strings.Contains(goal, "当前目录") ||
		strings.Contains(goal, "文件")
}

func createMinimalPlan(goal string) *ExecutionPlan {
	plan := NewExecutionPlan(generatePlanID(), goal)
	plan.Summary = buildMinimalSummary(goal)
	plan.Tasks["task_1"] = &Task{
		ID:          "task_1",
		Description: goal,
		TaskType:    inferSimpleTaskType(goal),
		TaskStatus:  TaskStatusPending,
		Args:        make(map[string]any),
		DependsOn:   make([]string, 0),
		DependedBy:  make([]string, 0),
	}
	_ = plan.ComputeExecutionOrder()
	return plan
}

func buildMinimalSummary(goal string) string {
	if goal == "" {
		return "执行简单任务"
	}
	return "直接执行简单任务: " + goal
}

func cloneArgs(args map[string]any) map[string]any {
	if len(args) == 0 {
		return make(map[string]any)
	}
	out := make(map[string]any, len(args))
	for key, value := range args {
		out[key] = value
	}
	return out
}

func inferSimpleTaskType(goal string) TaskType {
	if strings.Contains(goal, "读取") || strings.Contains(goal, "打开") ||
		(strings.Contains(goal, "查看") && strings.Contains(goal, "文件")) {
		return TaskTypeFileRead
	}
	if strings.Contains(goal, "写入") || strings.Contains(goal, "修改") || strings.Contains(goal, "创建文件") {
		return TaskTypeFileWrite
	}
	if strings.Contains(goal, "分析") || strings.Contains(goal, "总结") || strings.Contains(goal, "解释") {
		return TaskTypeAnalysis
	}
	if strings.Contains(goal, "验证") || strings.Contains(goal, "检查") {
		return TaskTypeVerification
	}
	return TaskTypeCommand
}
