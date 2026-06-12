package memory

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

type ContextCompressor struct {
	chatClient         ChatClient
	retainRecentRounds int
}

func NewContextCompressor(chatClient ChatClient, retainRecentRounds int) *ContextCompressor {
	if retainRecentRounds <= 0 {
		retainRecentRounds = 3
	}
	return &ContextCompressor{
		chatClient:         chatClient,
		retainRecentRounds: retainRecentRounds,
	}
}

func (c *ContextCompressor) SetChatClient(chatClient ChatClient) {
	c.chatClient = chatClient
}

func (c *ContextCompressor) Compress(memory *ConversationMemory) string {
	allEntries := memory.GetAll()
	if len(allEntries) <= c.retainRecentRounds {
		return ""
	}

	splitPoint := len(allEntries) - c.retainRecentRounds
	oldEntries := append([]MemoryEntry(nil), allEntries[:splitPoint]...)
	recentEntries := append([]MemoryEntry(nil), allEntries[splitPoint:]...)

	chunkSummaries := c.mapPhase(oldEntries)
	if len(chunkSummaries) == 0 {
		return ""
	}

	finalSummary := chunkSummaries[0]
	if len(chunkSummaries) > 1 {
		finalSummary = c.reducePhase(chunkSummaries)
	}

	memory.Clear()
	summaryEntry := NewMemoryEntry(
		"summary-"+randomSuffix(),
		"[historical conversation summary] "+finalSummary,
		MemoryTypeSummary,
		nil,
		EstimateTokens(finalSummary),
	)
	memory.Store(summaryEntry)
	for _, entry := range recentEntries {
		memory.Store(entry)
	}
	return finalSummary
}

func (c *ContextCompressor) ExtractFacts(entries []MemoryEntry, longTerm *LongTermMemory) []string {
	if len(entries) == 0 {
		return nil
	}

	if c.chatClient == nil {
		return c.extractFactsHeuristically(entries, longTerm)
	}

	var builder strings.Builder
	for _, entry := range entries {
		builder.WriteString(strings.ToUpper(resolveEntrySource(entry)))
		builder.WriteString("(")
		builder.WriteString(string(entry.Type))
		builder.WriteString("): ")
		builder.WriteString(entry.Content)
		builder.WriteString("\n\n")
	}

	response, err := c.chatClient.Chat([]Message{
		SystemMessage("Extract only durable facts, one fact per line."),
		UserMessage("Extract durable cross-session facts from:\n\n" + builder.String()),
	})
	if err != nil {
		return c.extractFactsHeuristically(entries, longTerm)
	}

	var facts []string
	for _, line := range strings.Split(response.Content, "\n") {
		fact := normalizeFactLine(line)
		if !isPersistentFactCandidate(fact) {
			continue
		}
		facts = append(facts, fact)
		if longTerm != nil {
			longTerm.Store(NewMemoryEntry(
				"fact-"+randomSuffix(),
				fact,
				MemoryTypeFact,
				map[string]string{"source": "fact_extractor"},
				EstimateTokens(fact),
			))
		}
	}
	return facts
}

func (c *ContextCompressor) mapPhase(oldEntries []MemoryEntry) []string {
	var summaries []string
	for _, chunk := range partitionMemoryEntries(oldEntries, 5) {
		var builder strings.Builder
		for _, entry := range chunk {
			builder.WriteString(string(entry.Type))
			builder.WriteString(": ")
			builder.WriteString(entry.Content)
			builder.WriteString("\n\n")
		}

		summary := c.summarizeChunk(builder.String())
		if strings.TrimSpace(summary) != "" {
			summaries = append(summaries, summary)
		}
	}
	return summaries
}

func (c *ContextCompressor) reducePhase(summaries []string) string {
	if len(summaries) == 1 {
		return summaries[0]
	}
	joined := strings.Join(summaries, "\n\n---\n\n")
	if c.chatClient == nil {
		return fallbackSummary(joined, 300)
	}
	response, err := c.chatClient.Chat([]Message{
		SystemMessage("Merge the summaries into one concise summary."),
		UserMessage(joined),
	})
	if err != nil || strings.TrimSpace(response.Content) == "" {
		return fallbackSummary(joined, 300)
	}
	return response.Content
}

func (c *ContextCompressor) summarizeChunk(chunkText string) string {
	if c.chatClient == nil {
		return fallbackSummary(chunkText, 200)
	}
	response, err := c.chatClient.Chat([]Message{
		SystemMessage("Summarize the conversation chunk and keep key intent, actions, decisions, and technical details."),
		UserMessage(chunkText),
	})
	if err != nil || strings.TrimSpace(response.Content) == "" {
		return fallbackSummary(chunkText, 200)
	}
	return response.Content
}

func (c *ContextCompressor) extractFactsHeuristically(entries []MemoryEntry, longTerm *LongTermMemory) []string {
	var facts []string
	for _, entry := range entries {
		if entry.Type == MemoryTypeFact && isPersistentFactCandidate(entry.Content) {
			facts = append(facts, entry.Content)
			if longTerm != nil {
				longTerm.Store(entry)
			}
		}
	}
	return facts
}

func partitionMemoryEntries(entries []MemoryEntry, size int) [][]MemoryEntry {
	if size <= 0 {
		size = 1
	}
	var parts [][]MemoryEntry
	for i := 0; i < len(entries); i += size {
		parts = append(parts, append([]MemoryEntry(nil), entries[i:min(i+size, len(entries))]...))
	}
	return parts
}

func fallbackSummary(text string, maxRunes int) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	return string(runes[:maxRunes]) + "..."
}

var ephemeralFactPrefixes = []string{
	"user wants", "user needs", "please", "create", "delete", "modify", "generate", "this task",
}

var speculationCues = []string{
	"maybe", "should", "guess", "speculate", "typo", "reminder",
}

var durableFactHints = []string{
	"prefer", "project", "repo", "path", "stack", "version", "model", "api", "config", "environment", "default",
}

func normalizeFactLine(line string) string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "- ")
	line = strings.TrimPrefix(line, "* ")
	return strings.TrimSpace(line)
}

func isPersistentFactCandidate(fact string) bool {
	if len([]rune(strings.TrimSpace(fact))) <= 5 {
		return false
	}
	lower := strings.ToLower(fact)
	for _, prefix := range ephemeralFactPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return false
		}
	}
	for _, cue := range speculationCues {
		if strings.Contains(lower, cue) {
			return false
		}
	}
	if strings.Contains(fact, ":") {
		return true
	}
	for _, hint := range durableFactHints {
		if strings.Contains(lower, hint) {
			return true
		}
	}
	return false
}

func resolveEntrySource(entry MemoryEntry) string {
	if source := strings.TrimSpace(entry.Metadata["source"]); source != "" {
		return source
	}
	switch {
	case strings.HasPrefix(entry.ID, "user-"):
		return "user"
	case strings.HasPrefix(entry.ID, "assistant-"):
		return "assistant"
	case strings.HasPrefix(entry.ID, "tool-"):
		return "tool"
	default:
		return "unknown"
	}
}

// idCounter 单调递增,保证同一进程内 randomSuffix 永不重复。
// 旧实现用 time.Now().UnixNano()%1e8,在循环里快速连续建条目时纳秒可能相同 →
// ID 碰撞 → LongTermMemory.Store 按 ID 去重时互相覆盖,静默丢数据。
var idCounter atomic.Uint64

// randomSuffix 返回进程内唯一的 ID 后缀。带纳秒时间戳是为了可读(便于看创建顺序),
// 真正的唯一性由原子计数器保证,与时钟精度无关。
func randomSuffix() string {
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), idCounter.Add(1))
}
