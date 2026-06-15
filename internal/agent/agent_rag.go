package agent

import (
	"context"
	"fmt"
	"os"

	"lioncli/internal/rag"
)

// IndexProject 对 path(空则当前工作目录)建立 RAG 代码索引,供 code_search 工具检索。
// 索引会遍历项目并对每个代码块调 embedding 服务,可能较慢;结果持久化在
// <UserConfigDir>/teacli/go-rag 下,按项目路径哈希分文件。embedder 为 nil 返回错误。
func (a *Agent) IndexProject(ctx context.Context, path string) (string, error) {
	if a.embedder == nil {
		return "", fmt.Errorf("embedding 未配置(请设置 EMBEDDING_* 环境变量)")
	}
	if path == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("无法确定当前目录: %w", err)
		}
		path = cwd
	}

	index := rag.NewCodeIndex(a.embedder)
	result, err := index.Index(ctx, path)
	if err != nil {
		return "", fmt.Errorf("索引失败: %w", err)
	}
	return result.Message, nil
}
