package plan

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

type fakeToolRunner struct {
	calls []toolCall
	err   error
}

type toolCall struct {
	name string
	args map[string]any
}

func (r *fakeToolRunner) ExecuteTool(_ context.Context, name string, args map[string]any) (string, error) {
	r.calls = append(r.calls, toolCall{name: name, args: cloneArgs(args)})
	if r.err != nil {
		return "", r.err
	}
	return "ran " + name, nil
}

func TestPlanExecutorExecutesTasksInDependencyOrder(t *testing.T) {
	plan := NewExecutionPlan("plan_1", "test")
	plan.Tasks["task_1"] = &Task{
		ID:          "task_1",
		Description: "inspect",
		TaskType:    TaskTypeAnalysis,
		TaskStatus:  TaskStatusPending,
	}
	plan.Tasks["task_2"] = &Task{
		ID:          "task_2",
		Description: "go test ./...",
		TaskType:    TaskTypeCommand,
		TaskStatus:  TaskStatusPending,
		DependsOn:   []string{"task_1"},
	}

	runner := &fakeToolRunner{}
	executor := NewPlanExecutor(runner)
	if err := executor.Execute(context.Background(), plan); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if plan.PlanStatus != PlanStatusCompleted {
		t.Fatalf("plan status = %s, want %s", plan.PlanStatus, PlanStatusCompleted)
	}
	if got, want := plan.Tasks["task_1"].TaskStatus, TaskStatusCompleted; got != want {
		t.Fatalf("task_1 status = %s, want %s", got, want)
	}
	if got, want := plan.Tasks["task_2"].TaskStatus, TaskStatusCompleted; got != want {
		t.Fatalf("task_2 status = %s, want %s", got, want)
	}
	if got, want := len(runner.calls), 1; got != want {
		t.Fatalf("tool calls = %d, want %d", got, want)
	}
	if got, want := runner.calls[0].name, "command_execute"; got != want {
		t.Fatalf("tool name = %q, want %q", got, want)
	}
	if got, want := runner.calls[0].args["command"], "go test ./..."; got != want {
		t.Fatalf("command arg = %v, want %v", got, want)
	}
}

func TestPlanExecutorStopsWhenTaskFails(t *testing.T) {
	plan := NewExecutionPlan("plan_1", "test")
	plan.Tasks["task_1"] = &Task{
		ID:         "task_1",
		TaskType:   TaskTypeCommand,
		TaskStatus: TaskStatusPending,
		Args:       map[string]any{"command": "fail"},
	}
	plan.Tasks["task_2"] = &Task{
		ID:         "task_2",
		TaskType:   TaskTypeAnalysis,
		TaskStatus: TaskStatusPending,
		DependsOn:  []string{"task_1"},
	}

	executor := NewPlanExecutor(&fakeToolRunner{err: fmt.Errorf("boom")})
	err := executor.Execute(context.Background(), plan)
	if err == nil {
		t.Fatal("Execute() error = nil, want error")
	}
	if got, want := plan.PlanStatus, PlanStatusFailed; got != want {
		t.Fatalf("plan status = %s, want %s", got, want)
	}
	if got, want := plan.Tasks["task_1"].TaskStatus, TaskStatusFailed; got != want {
		t.Fatalf("task_1 status = %s, want %s", got, want)
	}
	if got, want := plan.Tasks["task_2"].TaskStatus, TaskStatusPending; got != want {
		t.Fatalf("task_2 status = %s, want %s", got, want)
	}
}

func TestPlannerParsesToolMetadata(t *testing.T) {
	p := NewPlanner(nil, nil)
	plan, err := p.parsePlan("test", `{
		"summary": "do it",
		"tasks": [
			{
				"id": "step_1",
				"description": "read go.mod",
				"type": "FILE_READ",
				"tool_name": "read_file",
				"args": {"path": "go.mod"},
				"dependencies": []
			}
		]
	}`)
	if err != nil {
		t.Fatalf("parsePlan() error = %v", err)
	}

	task := plan.Tasks["task_1"]
	if task == nil {
		t.Fatal("task_1 missing")
	}
	if got, want := task.ToolName, "read_file"; got != want {
		t.Fatalf("ToolName = %q, want %q", got, want)
	}
	if !reflect.DeepEqual(task.Args, map[string]any{"path": "go.mod"}) {
		t.Fatalf("Args = %#v, want path arg", task.Args)
	}
}

func TestPlannerRejectsInvalidPlans(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr string
	}{
		{
			name:    "empty tasks",
			input:   `{"summary":"bad","tasks":[]}`,
			wantErr: "no tasks",
		},
		{
			name:    "empty id",
			input:   `{"tasks":[{"id":"","description":"x","type":"ANALYSIS","dependencies":[]}]}`,
			wantErr: "empty id",
		},
		{
			name:    "duplicate id",
			input:   `{"tasks":[{"id":"a","description":"x","type":"ANALYSIS","dependencies":[]},{"id":"a","description":"y","type":"ANALYSIS","dependencies":[]}]}`,
			wantErr: "duplicate task id",
		},
		{
			name:    "empty description",
			input:   `{"tasks":[{"id":"a","description":"","type":"ANALYSIS","dependencies":[]}]}`,
			wantErr: "empty description",
		},
		{
			name:    "unknown type",
			input:   `{"tasks":[{"id":"a","description":"x","type":"NOPE","dependencies":[]}]}`,
			wantErr: "unknown task type",
		},
		{
			name:    "unknown dependency",
			input:   `{"tasks":[{"id":"a","description":"x","type":"ANALYSIS","dependencies":["missing"]}]}`,
			wantErr: "unknown task",
		},
		{
			name:    "self dependency",
			input:   `{"tasks":[{"id":"a","description":"x","type":"ANALYSIS","dependencies":["a"]}]}`,
			wantErr: "cannot depend on itself",
		},
		{
			name:    "duplicate dependency",
			input:   `{"tasks":[{"id":"a","description":"x","type":"ANALYSIS","dependencies":[]},{"id":"b","description":"y","type":"ANALYSIS","dependencies":["a","a"]}]}`,
			wantErr: "duplicate dependency",
		},
	}

	p := NewPlanner(nil, nil)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := p.parsePlan("test", tt.input)
			if err == nil {
				t.Fatal("parsePlan() error = nil, want error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("parsePlan() error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestComputeExecutionOrderRejectsMissingDependency(t *testing.T) {
	plan := NewExecutionPlan("plan_1", "test")
	plan.Tasks["task_1"] = &Task{
		ID:         "task_1",
		TaskType:   TaskTypeAnalysis,
		TaskStatus: TaskStatusPending,
		DependsOn:  []string{"missing"},
	}

	err := plan.ComputeExecutionOrder()
	if err == nil {
		t.Fatal("ComputeExecutionOrder() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Fatalf("ComputeExecutionOrder() error = %q, want missing dependency detail", err.Error())
	}
	if plan.ExecutionOrder != nil {
		t.Fatalf("ExecutionOrder = %#v, want nil after failure", plan.ExecutionOrder)
	}
}
