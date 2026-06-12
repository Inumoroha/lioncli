package plan

import (
	"context"
	"fmt"
)

// ToolRunner is the small part of tool.ToolRegistry needed by the plan executor.
type ToolRunner interface {
	ExecuteTool(ctx context.Context, name string, args map[string]any) (string, error)
}

type TaskHandler func(ctx context.Context, plan *ExecutionPlan, task *Task) (string, error)

type PlanExecutor struct {
	toolRunner ToolRunner
	handlers   map[TaskType]TaskHandler
}

type ExecutorOption func(*PlanExecutor)

func NewPlanExecutor(toolRunner ToolRunner, opts ...ExecutorOption) *PlanExecutor {
	executor := &PlanExecutor{
		toolRunner: toolRunner,
		handlers:   make(map[TaskType]TaskHandler),
	}
	executor.registerDefaultHandlers()
	for _, opt := range opts {
		if opt != nil {
			opt(executor)
		}
	}
	return executor
}

func WithTaskHandler(taskType TaskType, handler TaskHandler) ExecutorOption {
	return func(executor *PlanExecutor) {
		if handler == nil {
			delete(executor.handlers, taskType)
			return
		}
		executor.handlers[taskType] = handler
	}
}

func (e *PlanExecutor) Execute(ctx context.Context, plan *ExecutionPlan) error {
	if plan == nil {
		return fmt.Errorf("plan cannot be nil")
	}
	if len(plan.Tasks) == 0 {
		return fmt.Errorf("plan has no tasks")
	}
	if len(plan.ExecutionOrder) == 0 {
		if err := plan.ComputeExecutionOrder(); err != nil {
			plan.MarkFailed()
			return err
		}
	}

	plan.MarkStarted()
	for _, taskID := range plan.ExecutionOrder {
		task := plan.Tasks[taskID]
		if task == nil {
			plan.MarkFailed()
			return fmt.Errorf("task %q not found", taskID)
		}
		if task.TaskStatus == "" {
			task.TaskStatus = TaskStatusPending
		}
		if !task.IsExecutable(plan.Tasks) {
			task.MarkFailed("task dependencies are not completed")
			plan.MarkFailed()
			return fmt.Errorf("task %q is not executable", task.ID)
		}

		handler := e.handlers[task.TaskType]
		if handler == nil {
			handler = e.handlers[TaskTypeAnalysis]
		}
		if handler == nil {
			plan.MarkFailed()
			return fmt.Errorf("no handler registered for task type %q", task.TaskType)
		}

		task.MarkStarted()
		result, err := handler(ctx, plan, task)
		if err != nil {
			task.MarkFailed(err.Error())
			plan.MarkFailed()
			return fmt.Errorf("task %q failed: %w", task.ID, err)
		}
		task.MarkCompleted(result)
	}

	plan.MarkCompleted()
	return nil
}

func (e *PlanExecutor) registerDefaultHandlers() {
	e.handlers[TaskTypeCommand] = e.toolTaskHandler("command_execute")
	e.handlers[TaskTypeFileRead] = e.toolTaskHandler("read_file")
	e.handlers[TaskTypeFileWrite] = e.toolTaskHandler("write_file")
	e.handlers[TaskTypePlanning] = localTaskHandler
	e.handlers[TaskTypeAnalysis] = localTaskHandler
	e.handlers[TaskTypeVerification] = localTaskHandler
}

func (e *PlanExecutor) toolTaskHandler(defaultToolName string) TaskHandler {
	return func(ctx context.Context, _ *ExecutionPlan, task *Task) (string, error) {
		if e.toolRunner == nil {
			return "", fmt.Errorf("tool runner is nil")
		}

		toolName := task.ToolName
		if toolName == "" {
			toolName = defaultToolName
		}
		if toolName == "" {
			return "", fmt.Errorf("task %q has no tool name", task.ID)
		}

		args := cloneArgs(task.Args)
		if task.TaskType == TaskTypeCommand && args["command"] == nil && task.Description != "" {
			args["command"] = task.Description
		}

		return e.toolRunner.ExecuteTool(ctx, toolName, args)
	}
}

func localTaskHandler(_ context.Context, _ *ExecutionPlan, task *Task) (string, error) {
	if task.Description == "" {
		return string(task.TaskType) + " task completed", nil
	}
	return string(task.TaskType) + " task completed: " + task.Description, nil
}
