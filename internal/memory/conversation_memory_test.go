package memory

import "testing"

func TestConversationMemoryEvictsOldest(t *testing.T) {
	t.Parallel()

	memory := NewConversationMemory(10)
	memory.Store(NewMemoryEntry("1", "aaaa", MemoryTypeConversation, nil, 6))
	memory.Store(NewMemoryEntry("2", "bbbb", MemoryTypeConversation, nil, 6))

	entries := memory.GetAll()
	if len(entries) != 1 || entries[0].ID != "2" {
		t.Fatalf("unexpected entries after eviction: %+v", entries)
	}
	if len(memory.EvictedEntries()) != 1 || memory.EvictedEntries()[0].ID != "1" {
		t.Fatalf("expected oldest entry to be retained in evicted list")
	}
}
