package task

import (
	"strings"
	"time"
)

type DurableTask struct {
	ID         string     `json:"id"`
	Status     Status     `json:"status"`
	Prompt     string     `json:"prompt"`
	Result     string     `json:"result,omitempty"`
	Error      string     `json:"error,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	DurationMS int64      `json:"duration_ms"`
}

func (t DurableTask) Terminal() bool {
	return t.Status.Terminal()
}

func (t DurableTask) ShortPrompt() string {
	normalized := strings.TrimSpace(strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ").Replace(t.Prompt))
	if len(normalized) <= 80 {
		return normalized
	}
	return normalized[:80] + "..."
}
