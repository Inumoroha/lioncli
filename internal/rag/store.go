package rag

import (
	"errors"
	"strings"
)

type StoreBackend string

const (
	StoreBackendJSON StoreBackend = "json"
)

type StoreOptions struct {
	Backend StoreBackend
}

type CodeStore interface {
	ClearProject() error
	InsertChunks(entries []CodeChunkEntry) error
	InsertRelations(relations []CodeRelation) error
	Search(queryEmbedding []float32, topK int) ([]SearchResult, error)
	SearchByKeyword(keyword string) ([]SearchResult, error)
	GetRelations(name string) ([]CodeRelation, error)
	GetOutgoingRelations(name string) ([]CodeRelation, error)
	GetStats() IndexStats
	Close() error
}

type StoreFactory func(projectPath string) (CodeStore, error)

func DefaultStoreFactory() StoreFactory {
	return MustNewStoreFactory(StoreOptions{Backend: StoreBackendJSON})
}

func MustNewStoreFactory(options StoreOptions) StoreFactory {
	factory, err := NewStoreFactory(options)
	if err != nil {
		panic(err)
	}
	return factory
}

// NewStoreFactory 目前只支持 JSON 后端(零依赖,随二进制即用)。
// 之前的 SQLite 后端依赖第三方 modernc.org/sqlite,已移除。
func NewStoreFactory(options StoreOptions) (StoreFactory, error) {
	backend := options.Backend
	if backend == "" {
		backend = StoreBackendJSON
	}

	switch backend {
	case StoreBackendJSON:
		return func(projectPath string) (CodeStore, error) {
			return NewVectorStore(projectPath)
		}, nil
	default:
		return nil, errors.New("unsupported store backend: " + strings.TrimSpace(string(backend)))
	}
}
