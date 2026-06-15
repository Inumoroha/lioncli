package task

import (
	"fmt"
	"strconv"
	"strings"
)

func HandleCommand(manager *Manager, payload string) string {
	normalized := strings.TrimSpace(payload)
	if normalized == "" {
		normalized = "list"
	}
	lower := strings.ToLower(normalized)
	switch {
	case lower == "list":
		return FormatList(manager.List(20))
	case strings.HasPrefix(lower, "list "):
		return FormatList(manager.List(parseLimit(strings.TrimSpace(normalized[5:]), 20)))
	case strings.HasPrefix(lower, "add "):
		task, err := manager.Enqueue(strings.TrimSpace(normalized[4:]))
		if err != nil {
			return "failed to enqueue task: " + err.Error()
		}
		return fmt.Sprintf("background task queued: %s\n/task log %s", task.ID, task.ID)
	case strings.HasPrefix(lower, "cancel "):
		id := strings.TrimSpace(normalized[7:])
		if manager.Cancel(id) {
			return "cancellation requested for background task " + id
		}
		return "no cancelable background task found: " + id
	case strings.HasPrefix(lower, "log "):
		id := strings.TrimSpace(normalized[4:])
		if task, ok := manager.Find(id); ok {
			return FormatLog(task)
		}
		return "background task not found: " + id
	default:
		return strings.TrimSpace(fmt.Sprintf(`unknown /task command: %s
available commands:
  /task
  /task list [N]
  /task add <task prompt>
  /task cancel <task_id>
  /task log <task_id>`, payload))
	}
}

func FormatList(tasks []DurableTask) string {
	if len(tasks) == 0 {
		return "no background tasks"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "recent background tasks (%d):\n", len(tasks))
	for _, task := range tasks {
		fmt.Fprintf(&b, "  %s  %s  %dms  %s\n", task.ID, task.Status, task.DurationMS, task.ShortPrompt())
	}
	return strings.TrimRight(b.String(), "\n")
}

func FormatLog(task DurableTask) string {
	var b strings.Builder
	fmt.Fprintf(&b, "background task %s\n", task.ID)
	fmt.Fprintf(&b, "status: %s\n", task.Status)
	fmt.Fprintf(&b, "created: %s\n", task.CreatedAt.Format("2006-01-02T15:04:05Z07:00"))
	if task.StartedAt != nil {
		fmt.Fprintf(&b, "started: %s\n", task.StartedAt.Format("2006-01-02T15:04:05Z07:00"))
	}
	if task.FinishedAt != nil {
		fmt.Fprintf(&b, "finished: %s (%dms)\n", task.FinishedAt.Format("2006-01-02T15:04:05Z07:00"), task.DurationMS)
	}
	fmt.Fprintf(&b, "\nprompt:\n%s\n", task.Prompt)
	if strings.TrimSpace(task.Error) != "" {
		fmt.Fprintf(&b, "\nerror:\n%s\n", task.Error)
	}
	if strings.TrimSpace(task.Result) != "" {
		fmt.Fprintf(&b, "\nresult:\n%s\n", task.Result)
	}
	return strings.TrimSpace(b.String())
}

func parseLimit(raw string, def int) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || parsed <= 0 {
		return def
	}
	return parsed
}
