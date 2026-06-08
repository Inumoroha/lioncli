package tool

import (
	"context"
	"fmt"
	"lioncli/internal/llm"
	"sort"
	"strings"
	"sync"
)

// ToolRegistry 统一管理工具的注册和执行。
// 本地工具（自动注册）和 MCP 适配过来的远程工具共用同一个池子，
// agent 只面对 ToolRegistry，不关心工具实际是本地实现还是走 MCP 子进程。
type ToolRegistry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewToolRegistry 创建一个 ToolRegistry，并把所有自动注册的本地工具填进去。
func NewToolRegistry() *ToolRegistry {
	out := &ToolRegistry{
		tools: make(map[string]Tool),
	}
	for _, tool := range sortedToolsAuto() {
		out.tools[tool.Name] = tool	
	}
	return out
}

// Register 加一个工具，重名返回错误而不是 panic，
// 适合运行时动态接入（比如 MCP server 工具）的场景。
func (r *ToolRegistry) RegisterTool(t Tool) error {
	// 1. 基础校验：名字不能为空，参数必须是 map[string]any。
	if t.Name == "" {
		return fmt.Errorf("【🔧】工具必须有名字!")
	}

	// 2. 执行函数必须有，工具才有意义。
	if t.Execute == nil {
		return fmt.Errorf("【🔧】工具%s必须有执行函数!", t.Name)
	}

	// 3. 加锁注册工具，防止并发注册。
	r.mu.Lock()
	defer r.mu.Unlock()

	// 4. 检查名字是否已存在,重名直接返回错误，防止覆盖已有工具。
	if _, exists := r.tools[t.Name]; exists {
		return fmt.Errorf("【🔧】工具%s已注册!", t.Name)
	}
	r.tools[t.Name] = t
	return nil
}


// IsRegistered 判断工具是否已注册。
func (r *ToolRegistry) IsRegistered(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.tools[name]
	return ok
}

// UnregisterToolsByPrefix 删除所有名字以 prefix 开头的工具，返回删除条数。
// 持写锁，确保 LLMTools()/Execute() 不会观察到中间态。
// 给 MCP 这类按前缀分组的外部工具用：刷新前一把抹掉旧条目，再批量重写。
func (r *ToolRegistry) UnregisterToolsByPrefix(prefix string) int {
	// 1. 基础校验：前缀不能为空。
	if prefix == "" {
		return 0
	}

	// 2. 加锁删除工具，防止并发删除。
	r.mu.Lock()
	defer r.mu.Unlock()

	// 3. 遍历所有工具，删除名字以 prefix 开头的工具。
		n := 0
	for name := range r.tools {
		if strings.HasPrefix(name, prefix) {
			delete(r.tools, name)
			n++
		}
	}
	return n
}

// GetAllTools 返回所有已注册工具，按 name 排序，避免 LLM 看到不稳定的工具顺序。
func (r *ToolRegistry) GetAllTools() []Tool {
	// 1. 加锁读取所有工具，防止并发修改。
	r.mu.RLock()
	defer r.mu.RUnlock()

	// 2. 按名字排序工具，返回排序后的工具切片。
	names := make([]string, 0, len(r.tools))
	for n := range r.tools {
		names = append(names, n)
	}
	sort.Strings(names)

	// 3. 返回排序后的工具切片。
	out := make([]Tool, 0, len(names))
	for _, n := range names {
		out = append(out, r.tools[n])
	}
	return out
}

// ToLLMTools 把内部 Tool 翻译成 llm.Tool，喂给 ChatRequest.Tools。
func (r *ToolRegistry) ToLLMTools() []llm.Tool {
	tools := r.GetAllTools()
	out := make([]llm.Tool, 0, len(tools))
	for _, t := range tools {
		out = append(out, llm.Tool{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.Parameters,
		})
	}
	return out
}

// ExecuteTool 根据工具名分发到对应 Executor。
func (r *ToolRegistry) ExecuteTool(ctx context.Context, name string, args map[string]any) (string, error) {
	r.mu.RLock()
	tool, exists := r.tools[name]
	r.mu.RUnlock()
	if !exists {
		return "", fmt.Errorf("【🔧】工具 %s 不存在", name)
	}
	return tool.Execute(ctx, args)
}