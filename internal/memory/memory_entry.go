package memory

import (
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"
)

type MemoryType string

const (
	MemoryTypeConversation MemoryType = "CONVERSATION"
	MemoryTypeFact         MemoryType = "FACT"
	MemoryTypeSummary      MemoryType = "SUMMARY"
	MemoryTypeToolResult   MemoryType = "TOOL_RESULT"
)

type MemoryEntry struct {
	ID         string            `json:"id"`
	Content    string            `json:"content"`
	Type       MemoryType        `json:"type"`
	Timestamp  time.Time         `json:"timestamp"`
	Metadata   map[string]string `json:"metadata"`
	TokenCount int               `json:"token_count"`
}

func NewMemoryEntry(id, content string, memoryType MemoryType, metadata map[string]string, tokenCount int) MemoryEntry {
	return NewMemoryEntryAt(id, content, memoryType, time.Now().UTC(), metadata, tokenCount)
}

func NewMemoryEntryAt(id, content string, memoryType MemoryType, timestamp time.Time, metadata map[string]string, tokenCount int) MemoryEntry {
	if timestamp.IsZero() {
		timestamp = time.Now().UTC()
	}
	if metadata == nil {
		metadata = map[string]string{}
	}
	return MemoryEntry{
		ID:         id,
		Content:    content,
		Type:       memoryType,
		Timestamp:  timestamp,
		Metadata:   metadata,
		TokenCount: tokenCount,
	}
}

func EstimateTokens(text string) int {
	if text == "" {
		return 0
	}
	var hanCount, otherCount int
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			hanCount++
		} else {
			otherCount++
		}
	}
	return int(math.Ceil(float64(hanCount)/1.5 + float64(otherCount)/4.0))
}

func (e MemoryEntry) String() string {
	content := e.Content
	if len([]rune(content)) > 80 {
		content = string([]rune(content)[:80]) + "..."
	}
	return fmt.Sprintf("[%s] %s: %s", e.Type, e.ID, strings.TrimSpace(content))
}
