package plan

import "time"

type TaskType string

const (
	TaskTypePlanning     TaskType = "PLANNING"
	TaskTypeFileRead     TaskType = "FILE_READING"
	TaskTypeFileWrite    TaskType = "FILE_WRITING"
	TaskTypeCommand      TaskType = "COMMAND"
	TaskTypeAnalysis     TaskType = "ANALYSIS"
	TaskTypeVerification TaskType = "VERIFICATION"
)

type TaskStatus string

const (
	TaskStatusPending   TaskStatus = "PENDING"
	TaskStatusRunning   TaskStatus = "RUNNING"
	TaskStatusCompleted TaskStatus = "COMPLETED"
	TaskStatusFailed    TaskStatus = "FAILED"
	TaskStatusSkipped   TaskStatus = "SKIPPED"
)

type Task struct {
	ID          string
	Description string
	TaskType    TaskType
	TaskStatus  TaskStatus
	ToolName    string
	Args        map[string]any
	Result      string
	Error       string

	DependsOn  []string
	DependedBy []string

	StartTime time.Time
	EndTime   time.Time
}

func (t *Task) MarkStarted() {
	t.TaskStatus = TaskStatusRunning
	t.StartTime = time.Now()
}

func (t *Task) MarkCompleted(result string) {
	t.TaskStatus = TaskStatusCompleted
	t.Result = result
	t.EndTime = time.Now()
}

func (t *Task) MarkFailed(error string) {
	t.TaskStatus = TaskStatusFailed
	t.Error = error
	t.EndTime = time.Now()
}

func (t *Task) IsExecutable(allTasks map[string]*Task) bool {
	if t == nil || t.TaskStatus != TaskStatusPending {
		return false
	}
	for _, depID := range t.DependsOn {
		depTask, exists := allTasks[depID]
		if !exists || depTask == nil {
			return false
		}
		if depTask.TaskStatus != TaskStatusCompleted {
			return false
		}
	}
	return true
}
