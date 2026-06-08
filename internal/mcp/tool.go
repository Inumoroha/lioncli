package mcp

import (
	"context"
	"fmt"
	"log"

	"lioncli/internal/llm"

	"github.com/mark3labs/mcp-go/client"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// toolPrefixSep 用于把 serverName 和原始名字拼接，避免不同 server 间冲突。
// tools / resources / prompts 共用一套前缀方案：mcp__<server>__<original>。
const toolPrefixSep = "__"

// KeyPrefix 是所有 MCP 工具/资源/prompt key 共有的前缀（"mcp__"）。
// 导出供 internal/tool 等下游按前缀做整批操作（比如 SyncMCP 时清掉旧条目）。
const KeyPrefix = "mcp" + toolPrefixSep

// keyOf 生成对外暴露的、可路由回 (server, original) 的键。
func keyOf(serverName, original string) string {
	return KeyPrefix + serverName + toolPrefixSep + original
}

// toolEntry 记录一个工具属于哪个 server，以及它在 server 内的原始名字。
// LLM 看到的工具名是带前缀的，调用时需要按 entry 路由回去。
type toolEntry struct {
	serverName   string
	originalName string
	llmTool      llm.Tool
}

func toLLMTool(serverName string, t mcplib.Tool) llm.Tool {
	return llm.Tool{
		Name:        keyOf(serverName, t.Name),
		Description: t.Description,
		Parameters:  toMapSchema(t.InputSchema),
	}
}

func toMapSchema(s mcplib.ToolInputSchema) map[string]interface{} {
	m := map[string]interface{}{
		"type": s.Type,
	}
	if s.Properties != nil {
		m["properties"] = s.Properties
	}
	if len(s.Required) > 0 {
		m["required"] = s.Required
	}
	if s.Defs != nil {
		m["$defs"] = s.Defs
	}
	if s.AdditionalProperties != nil {
		m["additionalProperties"] = s.AdditionalProperties
	}
	return m
}

// loadTools 拉取每个声明了 tools capability 的 server 的工具清单（分页），
// 写入 m.tools。Initialize 阶段一次性调用，每页写入持 m.mu 写锁。
func (m *MCPManager) loadTools(ctx context.Context) {
	log.Println("=== 🧭 正在获取所有 MCP Server 提供的工具列表 ===")
	for serverName, s := range m.servers {
		if s.capabilities.Tools == nil {
			continue
		}
		tools, err := listAllTools(ctx, s.client)
		if err != nil {
			log.Printf("❌ 获取 MCP Server '%s' 的工具列表失败: %v", serverName, err)
			continue
		}
		m.mu.Lock()
		for _, t := range tools {
			lt := toLLMTool(serverName, t)
			m.tools[lt.Name] = toolEntry{
				serverName:   serverName,
				originalName: t.Name,
				llmTool:      lt,
			}
			log.Printf("✅ MCP Server '%s' 提供工具: %s", serverName, lt.Name)
		}
		m.mu.Unlock()
	}
}

// listAllTools 走完所有分页，返回 server 上的全部工具。
// 上限 maxListPages 是防御性熔断：行为有问题的 server 可能给出固定的 NextCursor 让客户端死循环。
func listAllTools(ctx context.Context, c *client.Client) ([]mcplib.Tool, error) {
	var out []mcplib.Tool
	var cursor mcplib.Cursor
	for page := 0; page < maxListPages; page++ {
		req := mcplib.ListToolsRequest{}
		req.Params.Cursor = cursor
		res, err := c.ListTools(ctx, req)
		if err != nil {
			return nil, err
		}
		out = append(out, res.Tools...)
		if res.NextCursor == "" {
			return out, nil
		}
		cursor = res.NextCursor
	}
	return nil, fmt.Errorf("分页超出上限 %d，疑似 server 游标行为异常", maxListPages)
}

// AllLLMTools 返回 LLM 可调用的全部工具描述。
func (m *MCPManager) AllLLMTools() []llm.Tool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]llm.Tool, 0, len(m.tools))
	for _, e := range m.tools {
		out = append(out, e.llmTool)
	}
	return out
}

// CallTool 把 LLM 给出的带前缀名字路由到对应 server 调用。
// 工具自身返回 IsError=true 时，把它给出的文本一并塞进 error，避免 agent 丢失原因。
func (m *MCPManager) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	m.mu.RLock()
	entry, ok := m.tools[name]
	m.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	s, ok := m.servers[entry.serverName]
	if !ok {
		return "", fmt.Errorf("server %s not connected", entry.serverName)
	}

	req := mcplib.CallToolRequest{}
	req.Params.Name = entry.originalName
	req.Params.Arguments = args

	res, err := s.client.CallTool(ctx, req)
	if err != nil {
		return "", err
	}

	text := extractText(res.Content)
	if res.IsError {
		return text, fmt.Errorf("tool %s failed: %s", name, text)
	}
	return text, nil
}
