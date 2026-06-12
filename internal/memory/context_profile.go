package memory

import "fmt"

type ContextProfile struct {
	MaxContextWindow        int
	ShortTermMemoryBudget   int
	CompressionTriggerRatio float64
}

func DefaultContextProfile() ContextProfile {
	return ContextProfile{
		MaxContextWindow:        128000,
		ShortTermMemoryBudget:   12000,
		CompressionTriggerRatio: 0.9,
	}
}

func CustomContextProfile(contextWindow, shortTermBudget int) ContextProfile {
	profile := DefaultContextProfile()
	if contextWindow > 0 {
		profile.MaxContextWindow = contextWindow
	}
	if shortTermBudget > 0 {
		profile.ShortTermMemoryBudget = shortTermBudget
	}
	return profile
}

func (p ContextProfile) Summary() string {
	return fmt.Sprintf(
		"context window=%d, short-term budget=%d, compression trigger=%.0f%%",
		p.MaxContextWindow,
		p.ShortTermMemoryBudget,
		p.CompressionTriggerRatio*100,
	)
}
