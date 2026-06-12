package tool

import (
	"context"
	"encoding/json"
	"fmt"
)

// RegisterMCPHelpers 注册两个把 MCP 资源能力暴露给 LLM 的内置工具：
//
//   - mcp_list_resources：列出所有 MCP server 当前提供的资源（key/uri/name/...）。
//   - mcp_read_resource(key)：按 key 读取一个资源的内容（文本或 base64 blob）。
//
// 工具名故意用单下划线，不在 mcp.KeyPrefix（"mcp__"）开头，
// 所以 SyncMCP 因 list_changed 重写 MCP 工具时不会把它们清掉。
//
// 必须在 main.go 拿到 *mcp.MCPManager 之后调用，调一次即可。
// 后续 server 推送 resources/list_changed 时，AllResources 会自动给出新快照，
// 不需要重新注册这两个工具。
//
// 参数：
//   r    - ToolRegistry 指针，用于注册工具
//   mgr  - mcpInvoker 接口，提供资源查询和读取能力
//
// 返回值：
//   error - 注册失败时返回错误，成功返回 nil
func RegisterMCPHelpers(r *ToolRegistry, mgr mcpInvoker) error {
	// 参数校验：ToolRegistry 不能为空
	if r == nil {
		return fmt.Errorf("registry is nil")
	}
	// 参数校验：MCP 管理器不能为空
	if mgr == nil {
		return fmt.Errorf("mcp manager is nil")
	}

	// 注册资源列表工具
	if err := r.RegisterTool(newListResourcesTool(mgr)); err != nil {
		return fmt.Errorf("register mcp_list_resources: %w", err)
	}
	// 注册资源读取工具
	if err := r.RegisterTool(newReadResourceTool(mgr)); err != nil {
		return fmt.Errorf("register mcp_read_resource: %w", err)
	}
	return nil
}

// newListResourcesTool 创建一个用于列出所有 MCP 资源的工具
//
// 该工具允许 LLM 查询当前连接的所有 MCP server 提供的资源列表，
// 返回包含 key、uri、name、description、mimeType 等信息的 JSON 数组。
//
// 参数：
//   mgr - mcpInvoker 接口，用于获取所有资源
//
// 返回值：
//   Tool - 配置好的资源列表工具
func newListResourcesTool(mgr mcpInvoker) Tool {
	return Tool{
		Name: "mcp_list_resources",
		Description: "List all resources exposed by connected MCP servers. " +
			"Returns a JSON array of {key, uri, name, description, mimeType}. " +
			"Use the returned `key` with mcp_read_resource to fetch the content.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{}, // 无参数
		},
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			// 从 MCP 管理器获取所有资源
			resources := mgr.AllResources()
			// 处理空资源情况，返回友好提示
			if len(resources) == 0 {
				return "[]\n(no MCP resources available)", nil
			}
			// 将资源列表序列化为格式化的 JSON
			raw, err := json.MarshalIndent(resources, "", "  ")
			if err != nil {
				return "", fmt.Errorf("marshal resources: %w", err)
			}
			return string(raw), nil
		},
	}
}

// newReadResourceTool 创建一个用于读取 MCP 资源内容的工具
//
// 该工具允许 LLM 根据资源 key 读取指定 MCP 资源的内容，
// 返回包含 uri、mimeType、text、blob 等字段的 JSON 对象。
// 文本资源填充 text 字段，二进制资源填充 blob 字段（base64 编码）。
//
// 参数：
//   mgr - mcpInvoker 接口，用于读取资源内容
//
// 返回值：
//   Tool - 配置好的资源读取工具
func newReadResourceTool(mgr mcpInvoker) Tool {
	return Tool{
		Name: "mcp_read_resource",
		Description: "Read the contents of an MCP resource by its key " +
			"(obtain keys from mcp_list_resources). Returns a JSON array of " +
			"{uri, mimeType, text, blob}. Text resources fill `text`; " +
			"binary resources fill `blob` (base64).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"key": map[string]any{
					"type":        "string",
					"description": "The key returned by mcp_list_resources (e.g. \"mcp__filesystem__file:///path\").",
				},
			},
			"required": []string{"key"}, // 必填参数：资源 key
		},
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			// 从参数中提取 key
			key := StringArg(args, "key")
			// 参数校验：key 不能为空
			if key == "" {
				return "", fmt.Errorf("missing required parameter: key")
			}

			// 调用 MCP 管理器读取资源内容
			contents, err := mgr.ReadResource(ctx, key)
			if err != nil {
				// 同 read_file：失败信息也回给 LLM，方便它自己换 key 再试
				return fmt.Sprintf("failed to read resource %q: %v\nUse mcp_list_resources to discover valid keys.", key, err), nil
			}

			// 将资源内容序列化为格式化的 JSON
			raw, err := json.MarshalIndent(contents, "", "  ")
			if err != nil {
				return "", fmt.Errorf("marshal resource contents: %w", err)
			}

			// 限制返回内容长度，避免过大响应
			s := string(raw)
			if len(s) > 20000 {
				s = s[:20000] + "\n\n...[truncated]..."
			}
			return s, nil
		},
	}
}