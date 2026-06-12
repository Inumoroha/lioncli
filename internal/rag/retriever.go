package rag

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
)

type CodeRetriever struct {
	embedder Embedder
	store    CodeStore
}

func NewCodeRetriever(projectPath string, embedder Embedder) (*CodeRetriever, error) {
	return NewCodeRetrieverWithStoreFactory(projectPath, embedder, DefaultStoreFactory())
}

func NewCodeRetrieverWithStoreFactory(projectPath string, embedder Embedder, storeFactory StoreFactory) (*CodeRetriever, error) {
	if embedder == nil {
		embedder = NewEmbeddingClientFromEnv()
	}
	if storeFactory == nil {
		storeFactory = DefaultStoreFactory()
	}
	root, err := filepath.Abs(projectPath)
	if err != nil {
		return nil, err
	}
	store, err := storeFactory(root)
	if err != nil {
		return nil, err
	}
	return &CodeRetriever{
		embedder: embedder,
		store:    store,
	}, nil
}

func (r *CodeRetriever) SemanticSearch(ctx context.Context, query string, topK int) ([]SearchResult, error) {
	embedding, err := r.embedder.Embed(ctx, query)
	if err != nil {
		return nil, err
	}
	return r.store.Search(embedding, topK)
}

func (r *CodeRetriever) KeywordSearch(keyword string) ([]SearchResult, error) {
	return r.store.SearchByKeyword(keyword)
}

func (r *CodeRetriever) HybridSearch(ctx context.Context, query string, topK int) ([]SearchResult, error) {
	merged := make(map[string]SearchResult)
	dualMatchBonused := make(map[string]struct{})

	semanticLimit := topK * 2
	if semanticLimit < 10 {
		semanticLimit = 10
	}
	semanticResults, err := r.SemanticSearch(ctx, query, semanticLimit)
	if err != nil {
		return nil, err
	}
	for _, result := range semanticResults {
		mergeResult(merged, result, dualMatchBonused)
	}

	for _, keyword := range TokenizeQuery(query) {
		keywordResults, err := r.KeywordSearch(keyword)
		if err != nil {
			return nil, err
		}
		for _, result := range keywordResults {
			mergeResult(merged, boostKeywordMatch(result, keyword), dualMatchBonused)
		}
	}

	ranked := make([]SearchResult, 0, len(merged))
	for _, result := range merged {
		boost := 0.0
		switch result.ChunkType {
		case "method", "function":
			boost = 0.15
		case "type":
			boost = 0.10
		}
		result.Similarity += boost
		ranked = append(ranked, result)
	}

	sort.Slice(ranked, func(i, j int) bool {
		return ranked[i].Similarity > ranked[j].Similarity
	})
	return limitPerFile(ranked, topK, 2), nil
}

func (r *CodeRetriever) GetRelationGraph(name string) ([]CodeRelation, error) {
	return r.store.GetRelations(name)
}

func (r *CodeRetriever) GetStats() IndexStats {
	return r.store.GetStats()
}

func (r *CodeRetriever) Close() error {
	if r == nil || r.store == nil {
		return nil
	}
	return r.store.Close()
}

func mergeResult(merged map[string]SearchResult, candidate SearchResult, dualMatchBonused map[string]struct{}) {
	key := candidate.FilePath + "#" + candidate.Name
	existing, ok := merged[key]
	if !ok {
		merged[key] = candidate
		return
	}

	best := existing
	if candidate.Similarity > best.Similarity {
		best = candidate
	}
	if _, alreadyBonused := dualMatchBonused[key]; !alreadyBonused {
		best.Similarity += 0.1
		dualMatchBonused[key] = struct{}{}
	}
	merged[key] = best
}

func boostKeywordMatch(result SearchResult, keyword string) SearchResult {
	keyword = strings.ToLower(keyword)
	name := strings.ToLower(result.Name)
	file := strings.ToLower(result.FilePath)
	content := strings.ToLower(result.Content)

	bonus := 0.0
	if strings.Contains(name, keyword) {
		bonus += 0.3
	}
	if strings.Contains(file, keyword) {
		bonus += 0.1
	}
	if strings.Contains(content, keyword) {
		bonus += 0.1
	}
	result.Similarity += bonus
	return result
}

func limitPerFile(sorted []SearchResult, topK, maxPerFile int) []SearchResult {
	if topK <= 0 {
		return nil
	}
	counts := make(map[string]int)
	var results []SearchResult
	for _, result := range sorted {
		if counts[result.FilePath] >= maxPerFile {
			continue
		}
		counts[result.FilePath]++
		results = append(results, result)
		if len(results) >= topK {
			break
		}
	}
	return results
}
