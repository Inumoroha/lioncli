package memory

type Memory interface {
	Store(entry MemoryEntry)
	Retrieve(id string) (MemoryEntry, bool)
	Search(query string, limit int) []MemoryEntry
	GetAll() []MemoryEntry
	Delete(id string) bool
	Clear()
	TokenCount() int
	Size() int
}
