package mcp

import (
	"context"
	"fmt"
	"log"

	"github.com/mark3labs/mcp-go/client"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// Prompt 是对外暴露的、与 mcp 库类型解耦的 prompt 描述。
// Key 是带 server 前缀的稳定标识，用于在 GetPrompt 时路由回原始 (server, name)。
type Prompt struct {
	Key         string           `json:"key"`
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

// PromptArgument 描述一个 prompt 模板参数。
type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// PromptResult 是 GetPrompt 的返回结果。
// Messages 是经过模板渲染后的消息列表，调用方通常会把它直接 append 到 LLM 的对话中。
type PromptResult struct {
	Description string          `json:"description,omitempty"`
	Messages    []PromptMessage `json:"messages"`
}

// PromptMessage 简化了 mcp 协议里的多模态 content：这里只暴露文本，
// 对非文本内容（image/audio/embedded resource）按 JSON 序列化兜底。
// 上层 LLM 接入的也只是文本通道，更复杂的多模态等真有需求再扩展。
type PromptMessage struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

// promptEntry 记录一个 prompt 属于哪个 server，以及它在 server 内的原始名字。
type promptEntry struct {
	serverName   string
	originalName string
	prompt       Prompt
}

func toPrompt(serverName string, p mcplib.Prompt) Prompt {
	args := make([]PromptArgument, 0, len(p.Arguments))
	for _, a := range p.Arguments {
		args = append(args, PromptArgument{
			Name:        a.Name,
			Description: a.Description,
			Required:    a.Required,
		})
	}
	return Prompt{
		Key:         keyOf(serverName, p.Name),
		Name:        p.Name,
		Description: p.Description,
		Arguments:   args,
	}
}

func toPromptResult(r *mcplib.GetPromptResult) PromptResult {
	out := PromptResult{Description: r.Description}
	out.Messages = make([]PromptMessage, 0, len(r.Messages))
	for _, m := range r.Messages {
		out.Messages = append(out.Messages, PromptMessage{
			Role: string(m.Role),
			Text: extractPromptText(m.Content),
		})
	}
	return out
}

// extractPromptText 复用 manager 里 extractText 的思路，但参数是单个 Content。
// 非文本 content 走 JSON 兜底，避免上层拿到空字符串时不知道发生了什么。
func extractPromptText(c mcplib.Content) string {
	switch v := c.(type) {
	case mcplib.TextContent:
		return v.Text
	case *mcplib.TextContent:
		return v.Text
	}
	return extractText([]mcplib.Content{c})
}

// loadPrompts 拉取每个声明了 prompts capability 的 server 的 prompt 清单（分页），
// 写入 m.prompts。Initialize 阶段一次性调用，每页写入持 m.mu 写锁。
func (m *MCPManager) loadPrompts(ctx context.Context) {
	log.Println("=== 💬 正在获取所有 MCP Server 提供的 Prompt 列表 ===")
	for serverName, s := range m.servers {
		if s.capabilities.Prompts == nil {
			continue
		}
		prompts, err := listAllPrompts(ctx, s.client)
		if err != nil {
			log.Printf("❌ 获取 MCP Server '%s' 的 Prompt 列表失败: %v", serverName, err)
			continue
		}
		m.mu.Lock()
		for _, p := range prompts {
			pp := toPrompt(serverName, p)
			m.prompts[pp.Key] = promptEntry{
				serverName:   serverName,
				originalName: p.Name,
				prompt:       pp,
			}
			log.Printf("✅ MCP Server '%s' 提供 Prompt: %s", serverName, pp.Key)
		}
		m.mu.Unlock()
	}
}

// listAllPrompts 走完所有分页，返回 server 上的全部 prompt。见 listAllTools 的注释。
func listAllPrompts(ctx context.Context, c *client.Client) ([]mcplib.Prompt, error) {
	var out []mcplib.Prompt
	var cursor mcplib.Cursor
	for page := 0; page < maxListPages; page++ {
		req := mcplib.ListPromptsRequest{}
		req.Params.Cursor = cursor
		res, err := c.ListPrompts(ctx, req)
		if err != nil {
			return nil, err
		}
		out = append(out, res.Prompts...)
		if res.NextCursor == "" {
			return out, nil
		}
		cursor = res.NextCursor
	}
	return nil, fmt.Errorf("分页超出上限 %d，疑似 server 游标行为异常", maxListPages)
}

// AllPrompts 返回所有 server 提供的 prompt 描述（带前缀 key）。
func (m *MCPManager) AllPrompts() []Prompt {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Prompt, 0, len(m.prompts))
	for _, e := range m.prompts {
		out = append(out, e.prompt)
	}
	return out
}

// GetPrompt 通过带前缀的 key 渲染 prompt，路由到对应 server。
// args 是 prompt 模板参数，nil 表示静态 prompt。
func (m *MCPManager) GetPrompt(ctx context.Context, key string, args map[string]string) (PromptResult, error) {
	m.mu.RLock()
	entry, ok := m.prompts[key]
	m.mu.RUnlock()
	if !ok {
		return PromptResult{}, fmt.Errorf("unknown prompt: %s", key)
	}
	s, ok := m.servers[entry.serverName]
	if !ok {
		return PromptResult{}, fmt.Errorf("server %s not connected", entry.serverName)
	}

	req := mcplib.GetPromptRequest{}
	req.Params.Name = entry.originalName
	req.Params.Arguments = args

	res, err := s.client.GetPrompt(ctx, req)
	if err != nil {
		return PromptResult{}, err
	}
	return toPromptResult(res), nil
}
