package tui

import (
	"context"

	"lioncli/internal/browser"
	"lioncli/internal/hitl"
)

type Agent interface {
	SetHitl(hitl.HitlHandler)
	SetBrowserGuard(*browser.Guard)
	SetEvents(chan Event)
	TokenUsageCompact() string
	ToolSummaries() []string
	MemoryEnabled() bool
	MemoryStatus() string
	MemoryFacts() []string
	ClearShortTermMemory()
	ClearLongTermMemory()
	Plan(context.Context, string) (string, error)
	RunTeam(context.Context, string) (string, error)
	IndexProject(context.Context, string) (string, error)
	Run(context.Context, string) (string, error)
}

type EventKind string

const (
	EventAssistantText EventKind = "assistant_text"
	EventToolStart     EventKind = "tool_start"
	EventToolEnd       EventKind = "tool_end"
	EventError         EventKind = "error"
	EventInfo          EventKind = "info"
	EventPlanStart     EventKind = "plan_start"
	EventPlanReady     EventKind = "plan_ready"
	EventTaskStart     EventKind = "task_start"
	EventPlanDone      EventKind = "plan_done"
)

type Event struct {
	Kind       EventKind
	Text       string
	ToolID     string
	ToolName   string
	ToolInput  map[string]any
	ToolOutput string
	IsError    bool
	Err        error
}
