package memory

import (
	"path/filepath"
	"testing"
)

func TestLongTermMemoryPersistsEntries(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	memory, err := NewLongTermMemory(dir)
	if err != nil {
		t.Fatal(err)
	}
	memory.Store(NewMemoryEntry("fact-1", "project path: /tmp/app", MemoryTypeFact, nil, 5))

	reloaded, err := NewLongTermMemory(dir)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := reloaded.Retrieve("fact-1")
	if !ok || entry.Content != "project path: /tmp/app" {
		t.Fatalf("entry not reloaded: %+v, %v", entry, ok)
	}
	if filepath.Base(resolveLongTermStorageDir()) == "" {
		t.Fatal("storage dir resolution should not be empty")
	}
}
