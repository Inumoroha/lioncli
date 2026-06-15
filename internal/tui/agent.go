package tui

import (
	"context"

	coreagent "lioncli/internal/agent"
	"lioncli/internal/hitl"
)

type Agent interface {
	SetHitl(hitl.HitlHandler)
	SetBrowserGuard(hitl.ToolGuard)
	SetEvents(chan<- coreagent.Event)
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

type Event = coreagent.Event
type EventKind = coreagent.EventKind

const (
	EventAssistantText = coreagent.EventAssistantText
	EventToolStart     = coreagent.EventToolStart
	EventToolEnd       = coreagent.EventToolEnd
	EventError         = coreagent.EventError
	EventInfo          = coreagent.EventInfo
	EventPlanStart     = coreagent.EventPlanStart
	EventPlanReady     = coreagent.EventPlanReady
	EventTaskStart     = coreagent.EventTaskStart
	EventPlanDone      = coreagent.EventPlanDone
)
