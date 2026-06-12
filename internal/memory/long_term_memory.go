package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const longTermMemoryFilename = "long_term_memory.json"

// maxLongTermEntries 是长期记忆的容量上限。超出时按插入顺序淘汰最旧条目(FIFO)。
// 长期记忆只增不减会让 JSON 文件无限膨胀、每次全量重写越来越慢;给个上限把
// 文件大小和写入开销限制在可预期范围内。2000 条对单用户 CLI 已足够宽裕。
const maxLongTermEntries = 2000

type LongTermMemory struct {
	mu          sync.RWMutex
	entries     []MemoryEntry
	storageDir  string
	storagePath string
}

func NewLongTermMemory(storageDir string) (*LongTermMemory, error) {
	dir := strings.TrimSpace(storageDir)
	if dir == "" {
		dir = resolveLongTermStorageDir()
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create long-term memory dir: %w", err)
	}

	memory := &LongTermMemory{
		storageDir:  dir,
		storagePath: filepath.Join(dir, longTermMemoryFilename),
	}
	if err := memory.load(); err != nil {
		return nil, err
	}
	return memory, nil
}

func resolveLongTermStorageDir() string {
	if wd, err := os.Getwd(); err == nil && strings.TrimSpace(wd) != "" {
		return filepath.Join(wd, ".memory")
	}
	if dir, err := os.UserConfigDir(); err == nil && strings.TrimSpace(dir) != "" {
		return filepath.Join(dir, "teacli", "memory")
	}
	return ".memory"
}

func (m *LongTermMemory) Store(entry MemoryEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i, existing := range m.entries {
		if existing.ID == entry.ID {
			m.entries[i] = entry
			_ = m.persistLocked()
			return
		}
	}
	m.entries = append(m.entries, entry)
	// 超出容量上限时,从头部淘汰最旧条目(FIFO),保持文件大小有界。
	if len(m.entries) > maxLongTermEntries {
		m.entries = append([]MemoryEntry(nil), m.entries[len(m.entries)-maxLongTermEntries:]...)
	}
	_ = m.persistLocked()
}

func (m *LongTermMemory) Retrieve(id string) (MemoryEntry, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, entry := range m.entries {
		if entry.ID == id {
			return entry, true
		}
	}
	return MemoryEntry{}, false
}

func (m *LongTermMemory) Search(query string, limit int) []MemoryEntry {
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

func (m *LongTermMemory) GetAll() []MemoryEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]MemoryEntry(nil), m.entries...)
}

func (m *LongTermMemory) Delete(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i, entry := range m.entries {
		if entry.ID != id {
			continue
		}
		m.entries = append(m.entries[:i], m.entries[i+1:]...)
		_ = m.persistLocked()
		return true
	}
	return false
}

func (m *LongTermMemory) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = nil
	_ = m.persistLocked()
}

func (m *LongTermMemory) TokenCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	total := 0
	for _, entry := range m.entries {
		total += entry.TokenCount
	}
	return total
}

func (m *LongTermMemory) Size() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.entries)
}

func (m *LongTermMemory) StatusSummary() string {
	return fmt.Sprintf(
		"long-term memory: %d entries / %d tokens (storage: %s)",
		m.Size(),
		m.TokenCount(),
		m.storagePath,
	)
}

func (m *LongTermMemory) load() error {
	data, err := os.ReadFile(m.storagePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read long-term memory: %w", err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, &m.entries); err != nil {
		return fmt.Errorf("decode long-term memory: %w", err)
	}
	return nil
}

func (m *LongTermMemory) persistLocked() error {
	data, err := json.MarshalIndent(m.entries, "", "  ")
	if err != nil {
		return fmt.Errorf("encode long-term memory: %w", err)
	}
	if err := os.WriteFile(m.storagePath, data, 0o644); err != nil {
		return fmt.Errorf("write long-term memory: %w", err)
	}
	return nil
}
