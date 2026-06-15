package task

import "strings"

type Status string

const (
	StatusEnqueued  Status = "enqueued"
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusCanceled  Status = "canceled"
)

func ParseStatus(value string) Status {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(StatusRunning):
		return StatusRunning
	case string(StatusCompleted):
		return StatusCompleted
	case string(StatusFailed):
		return StatusFailed
	case string(StatusCanceled), "cancelled":
		return StatusCanceled
	default:
		return StatusEnqueued
	}
}

func (s Status) Terminal() bool {
	return s == StatusCompleted || s == StatusFailed || s == StatusCanceled
}
