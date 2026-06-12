package memory

import (
	"fmt"
	"sync"
)

// maxEvictedRetained 限制 evictedEntries 的长度。被驱逐的条目只保留最近这么多条,
// 供 compressor/状态检视;再旧的真正丢弃。没有上限的话,纯驱逐路径(token 超限但
// 未触发压缩)会让这个切片无限增长,形成内存泄漏。
const maxEvictedRetained = 50

type ConversationMemory struct {
	mu            sync.RWMutex
	entries       []MemoryEntry
	maxTokens     int
	currentTokens int
	// evictedEntries 是被驱逐出活动窗口的**原始**条目(注意:不是摘要),按驱逐顺序
	// 保留最近 maxEvictedRetained 条。旧名 compressedSummaries 名不副实——这些条目
	// 从未被压缩。压缩/注入摘要时清空(那时旧内容已被正式归纳)。
	evictedEntries []MemoryEntry
}

func NewConversationMemory(maxTokens int) *ConversationMemory {
	if maxTokens <= 0 {
		maxTokens = 8000
	}
	return &ConversationMemory{maxTokens: maxTokens}
}

func (m *ConversationMemory) Store(entry MemoryEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.entries = append(m.entries, entry)
	m.currentTokens += entry.TokenCount
	for m.currentTokens > m.maxTokens && len(m.entries) > 1 {
		m.evictOldestLocked()
	}
}

func (m *ConversationMemory) Retrieve(id string) (MemoryEntry, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, entry := range m.entries {
		if entry.ID == id {
			return entry, true
		}
	}
	return MemoryEntry{}, false
}

func (m *ConversationMemory) Search(query string, limit int) []MemoryEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	tokens := TokenizeMemoryQuery(query)
	var results []MemoryEntry
	for _, entry := range m.entries {
		if MemoryTextMatches(entry.Content, tokens) {
			results = append(results, entry)
			if limit > 0 && len(results) >= limit {
				break
			}
		}
	}
	return results
}

func (m *ConversationMemory) GetAll() []MemoryEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]MemoryEntry(nil), m.entries...)
}

func (m *ConversationMemory) Delete(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, entry := range m.entries {
		if entry.ID != id {
			continue
		}
		m.entries = append(m.entries[:i], m.entries[i+1:]...)
		m.currentTokens -= entry.TokenCount
		return true
	}
	return false
}

func (m *ConversationMemory) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = nil
	m.currentTokens = 0
	m.evictedEntries = nil
}

func (m *ConversationMemory) TokenCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.currentTokens
}

func (m *ConversationMemory) Size() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.entries)
}

func (m *ConversationMemory) MaxTokens() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.maxTokens
}

func (m *ConversationMemory) SetMaxTokens(maxTokens int) {
	if maxTokens <= 0 {
		panic("maxTokens must be positive")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.maxTokens = maxTokens
	for m.currentTokens > m.maxTokens && len(m.entries) > 1 {
		m.evictOldestLocked()
	}
}

// EvictedEntries 返回最近被驱逐出活动窗口的原始条目副本(非摘要)。
func (m *ConversationMemory) EvictedEntries() []MemoryEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]MemoryEntry(nil), m.evictedEntries...)
}

func (m *ConversationMemory) InjectSummary(summary MemoryEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.evictedEntries = nil
	m.entries = append(m.entries, summary)
	m.currentTokens += summary.TokenCount
}

func (m *ConversationMemory) UsageRatio() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.maxTokens <= 0 {
		return 0
	}
	return float64(m.currentTokens) / float64(m.maxTokens)
}

func (m *ConversationMemory) StatusSummary() string {
	return fmt.Sprintf(
		"short-term memory: %d entries / %d tokens (budget: %d, usage: %.0f%%, evicted: %d)",
		m.Size(),
		m.TokenCount(),
		m.MaxTokens(),
		m.UsageRatio()*100,
		len(m.EvictedEntries()),
	)
}

func (m *ConversationMemory) evictOldestLocked() {
	if len(m.entries) == 0 {
		return
	}
	oldest := m.entries[0]
	m.entries = append([]MemoryEntry(nil), m.entries[1:]...)
	m.currentTokens -= oldest.TokenCount
	m.evictedEntries = append(m.evictedEntries, oldest)
	// 只保留最近 maxEvictedRetained 条,丢弃更旧的,防止纯驱逐路径下无限增长。
	if len(m.evictedEntries) > maxEvictedRetained {
		m.evictedEntries = append([]MemoryEntry(nil), m.evictedEntries[len(m.evictedEntries)-maxEvictedRetained:]...)
	}
}
