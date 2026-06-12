package memory

import "fmt"

type TokenBudget struct {
	contextWindow          int
	reservedForSystem      int
	reservedForTools       int
	reservedForResponse    int
	totalInputTokens       int
	totalOutputTokens      int
	totalCachedInputTokens int
	llmCallCount           int
}

func NewTokenBudget(contextWindow int) *TokenBudget {
	return NewTokenBudgetWithReserve(contextWindow, 500, 800, 2000)
}

func NewTokenBudgetWithReserve(contextWindow, reservedForSystem, reservedForTools, reservedForResponse int) *TokenBudget {
	return &TokenBudget{
		contextWindow:       contextWindow,
		reservedForSystem:   reservedForSystem,
		reservedForTools:    reservedForTools,
		reservedForResponse: reservedForResponse,
	}
}

func (b *TokenBudget) AvailableForConversation() int {
	return b.contextWindow - b.reservedForSystem - b.reservedForTools - b.reservedForResponse
}

func (b *TokenBudget) IsWithinBudget(messages []Message) bool {
	return EstimateMessagesTokens(messages) <= b.AvailableForConversation()
}

func (b *TokenBudget) NeedsCompression(memory *ConversationMemory, triggerRatio float64) bool {
	compressionBudget := min(memory.MaxTokens(), b.AvailableForConversation())
	return float64(memory.TokenCount()) >= float64(compressionBudget)*triggerRatio
}

func (b *TokenBudget) RecordUsage(inputTokens, outputTokens int) {
	b.RecordUsageWithCached(inputTokens, outputTokens, 0)
}

func (b *TokenBudget) RecordUsageWithCached(inputTokens, outputTokens, cachedInputTokens int) {
	b.totalInputTokens += inputTokens
	b.totalOutputTokens += outputTokens
	if cachedInputTokens > 0 {
		b.totalCachedInputTokens += cachedInputTokens
	}
	b.llmCallCount++
}

func (b *TokenBudget) UsageReport() string {
	averageInput := 0.0
	if b.llmCallCount > 0 {
		averageInput = float64(b.totalInputTokens) / float64(b.llmCallCount)
	}
	return fmt.Sprintf(
		"token usage: calls %d | total input: %d | total output: %d | cached: %d | avg input: %.0f | budget: %d (available: %d)",
		b.llmCallCount,
		b.totalInputTokens,
		b.totalOutputTokens,
		b.totalCachedInputTokens,
		averageInput,
		b.contextWindow,
		b.AvailableForConversation(),
	)
}

// Compact 返回一行紧凑的 token 用量,供 TUI header 展示,如 "tok ↑12.3k ↓3.4k · 5 calls"。
func (b *TokenBudget) Compact() string {
	return fmt.Sprintf("tok ↑%s ↓%s · %d calls",
		humanK(b.totalInputTokens), humanK(b.totalOutputTokens), b.llmCallCount)
}

// humanK 把 token 数压成可读形式:>=1000 用 k(一位小数),否则原数。
func humanK(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%.1fk", float64(n)/1000)
}

func EstimateMessagesTokens(messages []Message) int {
	total := 0
	for _, msg := range messages {
		total += EstimateTokens(msg.Content)
		total += EstimateTokens(msg.Name)
		total += EstimateTokens(msg.ToolCallID)
		for _, toolCall := range msg.ToolCalls {
			total += EstimateTokens(toolCall.Function.Name)
			total += EstimateTokens(toolCall.Function.Arguments)
		}
	}
	total += len(messages) * 4
	return total
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
