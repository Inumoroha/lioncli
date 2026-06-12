package rag

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type IndexResult struct {
	ChunkCount    int
	RelationCount int
	FailedFiles   []string
	Message       string
}

type CodeIndex struct {
	embedder     Embedder
	chunker      *CodeChunker
	analyzer     *CodeAnalyzer
	storeFactory StoreFactory
}

func NewCodeIndex(embedder Embedder) *CodeIndex {
	return NewCodeIndexWithStoreFactory(embedder, DefaultStoreFactory())
}

func NewCodeIndexWithStoreFactory(embedder Embedder, storeFactory StoreFactory) *CodeIndex {
	if embedder == nil {
		embedder = NewEmbeddingClientFromEnv()
	}
	if storeFactory == nil {
		storeFactory = DefaultStoreFactory()
	}
	return &CodeIndex{
		embedder:     embedder,
		chunker:      NewCodeChunker(),
		analyzer:     NewCodeAnalyzer(),
		storeFactory: storeFactory,
	}
}

func (i *CodeIndex) Index(ctx context.Context, projectPath string) (IndexResult, error) {
	root, err := filepath.Abs(projectPath)
	if err != nil {
		return IndexResult{}, err
	}

	info, err := os.Stat(root)
	if err != nil {
		return IndexResult{}, err
	}
	if !info.IsDir() {
		return IndexResult{}, fmt.Errorf("project path is not a directory: %s", root)
	}

	files, err := collectIndexFiles(root)
	if err != nil {
		return IndexResult{}, err
	}

	var entries []CodeChunkEntry
	var relations []CodeRelation
	var types []GoTypeInfo
	var failed []string

	for _, file := range files {
		select {
		case <-ctx.Done():
			return IndexResult{}, ctx.Err()
		default:
		}

		chunks, err := i.chunker.ChunkFile(file)
		if err != nil {
			failed = append(failed, file+": "+err.Error())
			continue
		}
		chunksFailed := false
		fileEntries := make([]CodeChunkEntry, 0, len(chunks))
		for _, chunk := range chunks {
			embedding, err := i.embedder.Embed(ctx, chunk.ToEmbeddingText())
			if err != nil {
				failed = append(failed, file+": "+err.Error())
				chunksFailed = true
				break
			}
			fileEntries = append(fileEntries, CodeChunkEntry{Chunk: chunk, Embedding: embedding})
		}
		if chunksFailed {
			continue
		}
		entries = append(entries, fileEntries...)

		if strings.HasSuffix(strings.ToLower(file), ".go") {
			analysis, err := i.analyzer.AnalyzeFile(file)
			if err != nil {
				failed = append(failed, file+": "+err.Error())
				continue
			}
			relations = append(relations, analysis.Relations...)
			types = append(types, analysis.Types...)
		}
	}

	relations = append(relations, i.analyzer.InferImplementations(types)...)

	store, err := i.storeFactory(root)
	if err != nil {
		return IndexResult{}, err
	}
	defer store.Close()
	// 不需要先 ClearProject:InsertChunks / InsertRelations 都是整体替换,
	// 先 Clear 只会多一次全量写(还会瞬间清空已有索引)。
	if err := store.InsertChunks(entries); err != nil {
		return IndexResult{}, err
	}
	if err := store.InsertRelations(dedupeRelations(relations)); err != nil {
		return IndexResult{}, err
	}

	stats := store.GetStats()
	message := fmt.Sprintf("index complete: %d chunks, %d relations", stats.ChunkCount, stats.RelationCount)
	if len(failed) > 0 {
		message += fmt.Sprintf(", %d failed files", len(failed))
	}

	return IndexResult{
		ChunkCount:    stats.ChunkCount,
		RelationCount: stats.RelationCount,
		FailedFiles:   failed,
		Message:       message,
	}, nil
}

func collectIndexFiles(root string) ([]string, error) {
	var files []string
	skippedDirs := map[string]struct{}{
		".git":         {},
		".idea":        {},
		".vscode":      {},
		"node_modules": {},
		"target":       {},
		"build":        {},
		"dist":         {},
		"out":          {},
		"vendor":       {},
	}
	allowedExtensions := map[string]struct{}{
		".go":         {},
		".mod":        {},
		".sum":        {},
		".md":         {},
		".txt":        {},
		".json":       {},
		".yaml":       {},
		".yml":        {},
		".toml":       {},
		".proto":      {},
		".sql":        {},
		".sh":         {},
		".xml":        {},
		".properties": {},
	}

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if _, skip := skippedDirs[d.Name()]; skip || strings.HasPrefix(d.Name(), ".") && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		if _, ok := allowedExtensions[ext]; ok {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}
