package tool

import (
	"context"
	"fmt"
	"lioncli/internal/llm"
	"strings"

	"lioncli/internal/mcp"
)

// mcpInvoker 是 MCP 桥层的最小依赖契约。
// 单独抽出来一方面方便测试，另一方面避免 ToolRegistry / 内置桥工具跟整个mcp.MCPManager 强耦合。
// AllResources / ReadResource 给桥工具（mcp_list_resources、mcp_read_resource）用，让 LLM 也能消费 MCP 资源。
type mcpInvoker interface {
	AllLLMTools() []llm.Tool
	CallTool(ctx context.Context, name string, args map[string]any) (string, error)
	AllResources() []mcp.Resource
	ReadResource(ctx context.Context, key string) ([]mcp.ResourceContent, error)
}

// 编译期保证 mcp.MCPManager 满足 mcpInvoker。
var _ mcpInvoker = (*mcp.MCPManager)(nil)

// SyncMCP 把 Registry 中所有 MCP 工具替换成 mgr 当前提供的快照。
// 即先按 mcp.KeyPrefix 清空旧条目，再批量写入新条目。本地工具不受影响。
//
// 调用顺序很关键：先在不持 Registry 锁的情况下取 mgr.AllLLMTools()
// （它内部会取 MCPManager 的 RLock），再获取 Registry 写锁批量改写。
// 这样锁顺序是 [mcp.RLock → mcp.RUnlock] → [registry.Lock → registry.Unlock]，
// 永不交叉，无死锁可能。
func (r *ToolRegistry) SyncMCP(mgr mcpInvoker) error {
	if mgr == nil {
		return fmt.Errorf("mcp manager is nil")
	}
	tools := mgr.AllLLMTools()

	// 为什么这里需要加锁？
	// 因为在 SyncMCP 中，我们先清空所有 MCP 工具，再写入新的。
	// 如果不加锁，可能会在清空过程中被其他线程修改，导致数据不一致。
	r.mu.Lock()
	defer r.mu.Unlock()

	// 先清掉所有 MCP 工具，再写新的。
	for name := range r.tools {
		if strings.HasPrefix(name, mcp.KeyPrefix) {
			delete(r.tools, name)
		}
	}
	for _, t := range tools {
		// 显式拷贝以避免 Go 1.22 之前循环变量被闭包共享的坑。
		// 否则会导致在循环结束后，所有闭包都引用同一个变量，而不是每个工具的独立实例。
		name := t.Name
		r.tools[name] = Tool{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.Parameters,
			Execute: func(ctx context.Context, args map[string]any) (string, error) {
				return mgr.CallTool(ctx, name, args)
			},
		}
	}
	return nil
}

// RegisterMCP 是 SyncMCP 的别名，保留兼容旧调用点。
// Sync 语义包含了首次注册：清空 + 写入，对空 Registry 与对已有 MCP 工具的 Registry 行为一致。
func (r *ToolRegistry) RegisterMCP(mgr mcpInvoker) error {
	return r.SyncMCP(mgr)
}
