package rag

import (
	"context"
	"testing"
)

type stubEmbedder struct {
	vector []float32
}

func (s stubEmbedder) Embed(context.Context, string) ([]float32, error) {
	return s.vector, nil
}

type stubStore struct {
	semanticByQuery map[string][]SearchResult
	keywordByToken  map[string][]SearchResult
}

func (s *stubStore) ClearProject() error                                 { return nil }
func (s *stubStore) InsertChunks([]CodeChunkEntry) error                 { return nil }
func (s *stubStore) InsertRelations([]CodeRelation) error                { return nil }
func (s *stubStore) GetRelations(string) ([]CodeRelation, error)         { return nil, nil }
func (s *stubStore) GetOutgoingRelations(string) ([]CodeRelation, error) { return nil, nil }
func (s *stubStore) GetStats() IndexStats                                { return IndexStats{} }
func (s *stubStore) Close() error                                        { return nil }

func (s *stubStore) Search(queryEmbedding []float32, topK int) ([]SearchResult, error) {
	return append([]SearchResult(nil), s.semanticByQuery["default"]...), nil
}

func (s *stubStore) SearchByKeyword(keyword string) ([]SearchResult, error) {
	return append([]SearchResult(nil), s.keywordByToken[keyword]...), nil
}

func TestHybridSearchMergesAndLimitsPerFile(t *testing.T) {
	store := &stubStore{
		semanticByQuery: map[string][]SearchResult{
			"default": {
				{FilePath: "svc/a.go", ChunkType: "method", Name: "Foo.Parse", Content: "parse input", Similarity: 0.75},
				{FilePath: "svc/a.go", ChunkType: "method", Name: "Foo.Validate", Content: "validate input", Similarity: 0.55},
				{FilePath: "svc/a.go", ChunkType: "method", Name: "Foo.Render", Content: "render output", Similarity: 0.54},
				{FilePath: "svc/b.go", ChunkType: "type", Name: "Parser", Content: "parser type", Similarity: 0.50},
			},
		},
		keywordByToken: map[string][]SearchResult{
			"Foo.Parse": {
				{FilePath: "svc/a.go", ChunkType: "method", Name: "Foo.Parse", Content: "parse input", Similarity: 0.30},
			},
			"parser": {
				{FilePath: "svc/b.go", ChunkType: "type", Name: "Parser", Content: "parser type", Similarity: 0.30},
			},
		},
	}

	retriever := &CodeRetriever{
		embedder: stubEmbedder{vector: []float32{1, 0}},
		store:    store,
	}

	results, err := retriever.HybridSearch(context.Background(), "Foo.Parse parser", 3)
	if err != nil {
		t.Fatalf("HybridSearch returned error: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d: %#v", len(results), results)
	}
	if results[0].Name != "Foo.Parse" {
		t.Fatalf("expected Foo.Parse to rank first, got %#v", results[0])
	}

	perFileCount := map[string]int{}
	for _, result := range results {
		perFileCount[result.FilePath]++
	}
	if perFileCount["svc/a.go"] > 2 {
		t.Fatalf("expected max 2 results from the same file, got %v", perFileCount)
	}
}
