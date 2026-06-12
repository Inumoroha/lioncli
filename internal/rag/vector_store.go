package rag

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// CodeChunkEntry 代码分块和其嵌入向量的组合体
type CodeChunkEntry struct {
	Chunk     CodeChunk `json:"chunk"`
	Embedding []float32 `json:"embedding"`
}

// SearchResult 搜索结果数据模型
// 包含文件路径、分块类型、名称、内容和相似度
type SearchResult struct {
	FilePath   string  `json:"file_path"`
	ChunkType  string  `json:"chunk_type"`
	Name       string  `json:"name"`
	Content    string  `json:"content"`
	Similarity float64 `json:"similarity"`
}

// IndexStats 索引统计信息
// 包含分块数量和关系数量
type IndexStats struct {
	ChunkCount    int `json:"chunk_count"`
	RelationCount int `json:"relation_count"`
}

// vectorSnapshot 向量存储快照
// 包含项目路径、分块、关系和更新时间
type vectorSnapshot struct {
	ProjectPath string           `json:"project_path"`
	Chunks      []CodeChunkEntry `json:"chunks"`
	Relations   []CodeRelation   `json:"relations"`
	UpdatedAt   time.Time        `json:"updated_at"`
}

// VectorStore 向量存储
// 包含项目路径、存储路径和快照
// 用于存储和检索代码分块的向量表示
type VectorStore struct {
	projectPath string
	storePath   string
	snapshot    vectorSnapshot
}

func NewVectorStore(projectPath string) (*VectorStore, error) {
	normalized, err := filepath.Abs(projectPath)
	if err != nil {
		return nil, err
	}

	storeDir := defaultStoreDir()
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		return nil, err
	}

	hash := sha1.Sum([]byte(normalized))
	storePath := filepath.Join(storeDir, hex.EncodeToString(hash[:])+".json")
	store := &VectorStore{
		projectPath: normalized,
		storePath:   storePath,
		snapshot: vectorSnapshot{
			ProjectPath: normalized,
		},
	}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *VectorStore) ClearProject() error {
	s.snapshot.ProjectPath = s.projectPath
	s.snapshot.Chunks = nil
	s.snapshot.Relations = nil
	return s.save()
}

func (s *VectorStore) InsertChunks(entries []CodeChunkEntry) error {
	s.snapshot.ProjectPath = s.projectPath
	s.snapshot.Chunks = append([]CodeChunkEntry(nil), entries...)
	return s.save()
}

func (s *VectorStore) InsertRelations(relations []CodeRelation) error {
	s.snapshot.ProjectPath = s.projectPath
	s.snapshot.Relations = append([]CodeRelation(nil), relations...)
	return s.save()
}

func (s *VectorStore) Search(queryEmbedding []float32, topK int) ([]SearchResult, error) {
	if topK <= 0 {
		return nil, nil
	}

	// 维度不匹配通常意味着换了 embedding 模型却没重建索引:cosineSimilarity 会对
	// 每个 chunk 静默返回 0,检索结果全空且无报错,极难排查。这里显式告警一次。
	if len(s.snapshot.Chunks) > 0 && len(queryEmbedding) != len(s.snapshot.Chunks[0].Embedding) {
		fmt.Fprintf(os.Stderr,
			"⚠ embedding 维度不匹配 (query=%d, 索引=%d):疑似更换了 embedding 模型却未重建索引,检索结果将全部为 0。请重新建立索引。\n",
			len(queryEmbedding), len(s.snapshot.Chunks[0].Embedding))
	}

	results := make([]SearchResult, 0, len(s.snapshot.Chunks))
	for _, entry := range s.snapshot.Chunks {
		similarity := cosineSimilarity(queryEmbedding, entry.Embedding)
		results = append(results, SearchResult{
			FilePath:   entry.Chunk.FilePath,
			ChunkType:  entry.Chunk.ChunkType,
			Name:       entry.Chunk.Name,
			Content:    entry.Chunk.Content,
			Similarity: similarity,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Similarity > results[j].Similarity
	})
	if len(results) > topK {
		return append([]SearchResult(nil), results[:topK]...), nil
	}
	return results, nil
}

func (s *VectorStore) SearchByKeyword(keyword string) ([]SearchResult, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return nil, nil
	}
	lower := strings.ToLower(keyword)
	var results []SearchResult
	for _, entry := range s.snapshot.Chunks {
		name := strings.ToLower(entry.Chunk.Name)
		content := strings.ToLower(entry.Chunk.Content)
		if strings.Contains(name, lower) || strings.Contains(content, lower) {
			results = append(results, SearchResult{
				FilePath:   entry.Chunk.FilePath,
				ChunkType:  entry.Chunk.ChunkType,
				Name:       entry.Chunk.Name,
				Content:    entry.Chunk.Content,
				Similarity: 0.3,
			})
		}
	}
	return results, nil
}

func (s *VectorStore) GetRelations(name string) ([]CodeRelation, error) {
	var results []CodeRelation
	for _, relation := range s.snapshot.Relations {
		if relation.FromName == name || relation.ToName == name {
			results = append(results, relation)
		}
	}
	return results, nil
}

func (s *VectorStore) GetOutgoingRelations(name string) ([]CodeRelation, error) {
	var results []CodeRelation
	for _, relation := range s.snapshot.Relations {
		if relation.FromName == name {
			results = append(results, relation)
		}
	}
	return results, nil
}

func (s *VectorStore) GetStats() IndexStats {
	return IndexStats{
		ChunkCount:    len(s.snapshot.Chunks),
		RelationCount: len(s.snapshot.Relations),
	}
}

func (s *VectorStore) load() error {
	data, err := os.ReadFile(s.storePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var snapshot vectorSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return err
	}
	if snapshot.ProjectPath != "" && snapshot.ProjectPath != s.projectPath {
		return nil
	}
	s.snapshot = snapshot
	if s.snapshot.ProjectPath == "" {
		s.snapshot.ProjectPath = s.projectPath
	}
	return nil
}

func (s *VectorStore) save() error {
	s.snapshot.ProjectPath = s.projectPath
	s.snapshot.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(s.snapshot, "", "  ")
	if err != nil {
		return err
	}
	// 原子写:先写临时文件再 rename,避免全量重写中途崩溃把索引文件写坏/截断。
	tmp := s.storePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.storePath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func defaultStoreDir() string {
	if dir := os.Getenv("GO_RAG_STORE_DIR"); dir != "" {
		return dir
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return ".go-rag"
	}
	return filepath.Join(configDir, "teacli", "go-rag")
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		// 先各自转 float64 再运算:float32 相乘会在高维向量上累积精度损失。
		av := float64(a[i])
		bv := float64(b[i])
		dot += av * bv
		normA += av * av
		normB += bv * bv
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// vector_store_adapter
func (s *VectorStore) Close() error {
	return nil
}
