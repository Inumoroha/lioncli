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

// toLLMTool 将 MCP 协议定义的工具转换为 LLM 工具格式。
// 通过在工具名称前添加服务器名称前缀，确保不同服务器的同名工具不会冲突。
// 参数:
//   serverName - MCP服务器名称
//   t - MCP协议定义的工具对象
// 返回:
//   llm.Tool - LLM可识别的工具格式
func toLLMTool(serverName string, t mcplib.Tool) llm.Tool {
	return llm.Tool{
		Name:        keyOf(serverName, t.Name), // 使用keyOf生成带服务器前缀的唯一名称
		Description: t.Description,             // 直接使用原工具描述
		Parameters:  toMapSchema(t.InputSchema), // 将输入模式转换为LLM参数格式
	}
}

// toMapSchema 将 MCP 工具输入模式转换为 LLM 可识别的 JSON Schema 格式。
// 只包含非空字段，避免传递不必要的空值给 LLM。
// 参数:
//   s - MCP协议定义的工具输入模式
// 返回:
//   map[string]interface{} - JSON Schema 格式的参数定义
func toMapSchema(s mcplib.ToolInputSchema) map[string]interface{} {
	m := map[string]interface{}{
		"type": s.Type, // JSON Schema 类型
	}
	if s.Properties != nil {
		m["properties"] = s.Properties // 属性定义
	}
	if len(s.Required) > 0 {
		m["required"] = s.Required // 必填字段列表
	}
	if s.Defs != nil {
		m["$defs"] = s.Defs // 可复用的定义引用
	}
	if s.AdditionalProperties != nil {
		m["additionalProperties"] = s.AdditionalProperties // 额外属性约束
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
// 参数 ctx: 上下文，用于控制调用超时和取消
// 参数 c: MCP 客户端，用于与 server 通信
// 返回值: 所有工具列表，以及可能的错误
func listAllTools(ctx context.Context, c *client.Client) ([]mcplib.Tool, error) {
	// 初始化结果切片和分页游标
	var out []mcplib.Tool
	var cursor mcplib.Cursor

	// 循环获取所有分页，最多遍历 maxListPages 页（防御性熔断）
	for page := 0; page < maxListPages; page++ {
		// 构造分页请求
		req := mcplib.ListToolsRequest{}
		req.Params.Cursor = cursor

		// 调用 server 的 ListTools 接口
		res, err := c.ListTools(ctx, req)
		if err != nil {
			return nil, err
		}

		// 将当前页的工具追加到结果中
		out = append(out, res.Tools...)

		// 如果没有下一页游标，说明已经获取完所有工具
		if res.NextCursor == "" {
			return out, nil
		}

		// 更新游标，准备获取下一页
		cursor = res.NextCursor
	}

	// 超出分页上限，返回错误（疑似 server 游标行为异常）
	return nil, fmt.Errorf("分页超出上限 %d，疑似 server 游标行为异常", maxListPages)
}

// AllLLMTools 返回 LLM 可调用的全部工具描述。
// 该方法是线程安全的，通过读锁保护共享数据
// 返回值: 所有已加载工具的 llm.Tool 描述列表
func (m *MCPManager) AllLLMTools() []llm.Tool {
	// 获取读锁，确保并发安全
	m.mu.RLock()
	defer m.mu.RUnlock()

	// 预分配结果切片容量，优化性能
	out := make([]llm.Tool, 0, len(m.tools))

	// 遍历所有工具条目，收集 LLM 可用的工具描述
	for _, e := range m.tools {
		out = append(out, e.llmTool)
	}

	return out
}

// CallTool 把 LLM 给出的带前缀名字路由到对应 server 调用。
// 工具自身返回 IsError=true 时，把它给出的文本一并塞进 error，避免 agent 丢失原因。
// 参数 ctx: 上下文，用于控制调用超时和取消
// 参数 name: LLM 看到的带前缀的工具名（格式: mcp__<server>__<original>）
// 参数 args: 工具调用的参数映射
// 返回值: 工具执行结果文本，以及可能的错误
func (m *MCPManager) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	// 读取工具映射，查找对应的工具条目
	m.mu.RLock()
	entry, ok := m.tools[name]
	m.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", name)
	}

	// 查找工具所属的服务器连接
	s, ok := m.servers[entry.serverName]
	if !ok {
		return "", fmt.Errorf("server %s not connected", entry.serverName)
	}

	// 构造工具调用请求，使用原始工具名（不带前缀）
	req := mcplib.CallToolRequest{}
	req.Params.Name = entry.originalName
	req.Params.Arguments = args

	// 调用远程服务器的工具
	res, err := s.client.CallTool(ctx, req)
	if err != nil {
		return "", err
	}

	// 提取返回内容中的文本
	text := extractText(res.Content)

	// 如果工具返回错误，将结果文本包装进 error 返回
	if res.IsError {
		return text, fmt.Errorf("tool %s failed: %s", name, text)
	}

	return text, nil
}