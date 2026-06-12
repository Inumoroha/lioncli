package plan

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

type PlanStatus string

const (
	PlanStatusCreated   PlanStatus = "CREATED"
	PlanStatusRunning   PlanStatus = "RUNNING"
	PlanStatusCompleted PlanStatus = "COMPLETED"
	PlanStatusFailed    PlanStatus = "FAILED"
	PlanStatusCanceled  PlanStatus = "CANCELED"
)

type ExecutionPlan struct {
	ID             string
	Goal           string
	Tasks          map[string]*Task
	ExecutionOrder []string
	PlanStatus     PlanStatus
	Summary        string
	startTime      time.Time
	endTime        time.Time
}

func NewExecutionPlan(id, goal string) *ExecutionPlan {
	return &ExecutionPlan{
		ID:             id,
		Goal:           goal,
		Tasks:          make(map[string]*Task),
		ExecutionOrder: make([]string, 0),
		PlanStatus:     PlanStatusCreated,
	}
}

func (p *ExecutionPlan) ComputeExecutionOrder() error {
	if p == nil {
		return fmt.Errorf("plan cannot be nil")
	}
	if p.Tasks == nil {
		return fmt.Errorf("plan tasks cannot be nil")
	}

	p.ExecutionOrder = make([]string, 0, len(p.Tasks))
	state := make(map[string]int, len(p.Tasks))
	stack := make([]string, 0, len(p.Tasks))

	var dfs func(taskID string) error
	dfs = func(taskID string) error {
		switch state[taskID] {
		case 1:
			return fmt.Errorf("cycle detected at task %q via %s", taskID, strings.Join(append(stack, taskID), " -> "))
		case 2:
			return nil
		}

		task, exists := p.Tasks[taskID]
		if !exists || task == nil {
			return fmt.Errorf("task %q does not exist", taskID)
		}

		state[taskID] = 1
		stack = append(stack, taskID)
		defer func() {
			stack = stack[:len(stack)-1]
		}()

		seenDeps := make(map[string]struct{}, len(task.DependsOn))
		for _, depID := range task.DependsOn {
			depID = strings.TrimSpace(depID)
			if depID == "" {
				return fmt.Errorf("task %q has empty dependency id", taskID)
			}
			if _, ok := seenDeps[depID]; ok {
				return fmt.Errorf("task %q has duplicate dependency %q", taskID, depID)
			}
			seenDeps[depID] = struct{}{}
			if _, ok := p.Tasks[depID]; !ok {
				return fmt.Errorf("task %q depends on missing task %q", taskID, depID)
			}
			if err := dfs(depID); err != nil {
				return err
			}
		}

		state[taskID] = 2
		p.ExecutionOrder = append(p.ExecutionOrder, taskID)
		return nil
	}

	taskIDs := make([]string, 0, len(p.Tasks))
	for taskID := range p.Tasks {
		taskIDs = append(taskIDs, taskID)
	}
	sort.Strings(taskIDs)

	for _, taskID := range taskIDs {
		if state[taskID] == 0 {
			if err := dfs(taskID); err != nil {
				p.ExecutionOrder = nil
				return err
			}
		}
	}
	return nil
}

func (ep *ExecutionPlan) MarkStarted() {
	ep.PlanStatus = PlanStatusRunning
	ep.startTime = time.Now()
}

func (ep *ExecutionPlan) MarkCompleted() {
	ep.PlanStatus = PlanStatusCompleted
	ep.endTime = time.Now()
}

func (ep *ExecutionPlan) MarkFailed() {
	ep.PlanStatus = PlanStatusFailed
	ep.endTime = time.Now()
}

func (ep *ExecutionPlan) IsFailed() bool {
	if ep == nil {
		return false
	}
	for _, task := range ep.Tasks {
		if task != nil && task.TaskStatus == TaskStatusFailed {
			return true
		}
	}
	return false
}

func (ep *ExecutionPlan) Visualize() string {
	if ep == nil {
		return ""
	}

	var builder strings.Builder
	builder.WriteString("Execution Plan\n")
	builder.WriteString("ID: ")
	builder.WriteString(ep.ID)
	builder.WriteString("\nGoal: ")
	builder.WriteString(ep.Goal)
	builder.WriteString("\n")

	if strings.TrimSpace(ep.Summary) != "" {
		builder.WriteString("Summary: ")
		builder.WriteString(ep.Summary)
		builder.WriteString("\n")
	}

	builder.WriteString("Tasks:\n")
	for _, taskID := range ep.ExecutionOrder {
		task := ep.Tasks[taskID]
		if task == nil {
			continue
		}

		builder.WriteString("- ")
		builder.WriteString(task.ID)
		builder.WriteString(" [")
		builder.WriteString(string(task.TaskType))
		builder.WriteString("] ")
		builder.WriteString(task.Description)
		if len(task.DependsOn) > 0 {
			builder.WriteString(" (depends on: ")
			builder.WriteString(strings.Join(task.DependsOn, ", "))
			builder.WriteString(")")
		}
		builder.WriteString("\n")
	}

	return strings.TrimSpace(builder.String())
}
