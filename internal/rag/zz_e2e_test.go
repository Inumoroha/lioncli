package rag

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// 临时端到端:stub embedder + 真实 JSON store(临时目录),走 index→retrieve→format。
type fixedEmbedder struct{}

func (fixedEmbedder) Embed(context.Context, string) ([]float32, error) {
	return []float32{0.1, 0.2, 0.3}, nil
}

func TestZZIndexRetrieveFormat(t *testing.T) {
	// 隔离 JSON store 到临时目录,避免污染真实 <UserConfigDir>/teacli/go-rag。
	t.Setenv("GO_RAG_STORE_DIR", t.TempDir())

	// 造一个小项目目录,放一个 Go 文件。
	proj := t.TempDir()
	src := `package demo

// ParseConfig 解析配置文件。
func ParseConfig(path string) error {
	return nil
}

type Config struct {
	Name string
}
`
	if err := os.WriteFile(filepath.Join(proj, "demo.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	emb := fixedEmbedder{}

	// 索引。
	result, err := NewCodeIndex(emb).Index(ctx, proj)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	t.Logf("索引结果: %s", result.Message)
	if result.ChunkCount == 0 {
		t.Fatalf("应至少索引出若干代码块, got %d", result.ChunkCount)
	}

	// 检索。
	retr, err := NewCodeRetriever(proj, emb)
	if err != nil {
		t.Fatalf("NewCodeRetriever: %v", err)
	}
	defer retr.Close()

	results, err := retr.HybridSearch(ctx, "解析配置 ParseConfig", 5)
	if err != nil {
		t.Fatalf("HybridSearch: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("应召回至少一个代码块")
	}

	// 格式化。
	out := FormatForTool("解析配置", results)
	t.Logf("\n===== code_search 输出 =====\n%s", out)
	if out == "" {
		t.Fatal("FormatForTool 输出不应为空")
	}
}
