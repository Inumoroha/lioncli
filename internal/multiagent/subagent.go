package multiagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"lioncli/internal/hitl"
	"lioncli/internal/llm"
	"lioncli/internal/tool"
)

// maxToolIterations 限制单个子代理一次任务里的工具调用轮数,防止 LLM 陷入工具循环。
const maxToolIterations = 12

// SubAgent 是一个绑定了角色的轻量 LLM 代理:有自己的 system 人格,按需调用工具。
//
// 它是无状态的——每次 Run 用全新的对话历史,任务所需上下文全部由 orchestrator 在
// 输入里给足。这样同一个 worker 复用于多个步骤时不会串台(对应原设计"避免历史竞争")。
type SubAgent struct {
	name     string
	role     AgentRole
	client   llm.Client
	model    string
	registry *tool.ToolRegistry // 仅 useTools 时使用
	gate     hitl.HitlHandler  // 工具审批闸门,可为 nil
	guard    hitl.ToolGuard    // 浏览器操作护栏,可为 nil
	useTools bool              // 执行者为 true;规划者/检查者为 false(只思考)
	observer Observer

	// systemOverride 非空时覆盖角色默认 system prompt。
	// 用于"汇总"这类不属于三种固定角色人格的内部任务(避免误用规划者的 JSON 输出约束)。
	systemOverride string
}

// NewSubAgent 构造一个子代理。registry/gate/guard 可为 nil。
func NewSubAgent(name string, role AgentRole, client llm.Client, model string, registry *tool.ToolRegistry, gate hitl.HitlHandler, guard hitl.ToolGuard, useTools bool) *SubAgent {
	return &SubAgent{
		name:     name,
		role:     role,
		client:   client,
		model:    model,
		registry: registry,
		gate:     gate,
		guard:    guard,
		useTools: useTools && registry != nil,
	}
}

func (s *SubAgent) setObserver(o Observer) { s.observer = o }

func (s *SubAgent) withSystem(prompt string) *SubAgent {
	s.systemOverride = prompt
	return s
}

func (s *SubAgent) emit(e Event) {
	if s.observer != nil {
		e.Agent = s.name
		e.Role = s.role
		s.observer(e)
	}
}

// Run 让子代理处理一条输入,跑完整的"对话+工具调用"循环,返回最终文本。
func (s *SubAgent) Run(ctx context.Context, input string) (string, error) {
	system := s.role.SystemPrompt()
	if s.systemOverride != "" {
		system = s.systemOverride
	}
	history := []llm.Message{{
		Role:    llm.RoleUser,
		Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: input}},
	}}

	var tools []llm.Tool
	if s.useTools {
		tools = s.registry.ToLLMTools()
	}

	for i := 0; i < maxToolIterations; i++ {
		resp, err := s.client.Chat(ctx, llm.ChatRequest{
			Model:    s.model,
			System:   system,
			Messages: history,
			Tools:    tools,
		})
		if err != nil {
			return "", err
		}
		history = append(history, llm.Message{Role: llm.RoleAssistant, Content: resp.Content})

		if resp.StopReason != llm.StopReasonToolUse {
			return extractText(resp.Content), nil
		}

		toolResults := s.execTools(ctx, resp.Content)
		history = append(history, llm.Message{Role: llm.RoleTool, Content: toolResults})
	}
	return "", fmt.Errorf("子代理 %s 超过最大工具迭代次数(%d)", s.name, maxToolIterations)
}

// execTools 执行一轮 assistant 提出的所有工具调用,逐个过 HITL 审批闸门。
func (s *SubAgent) execTools(ctx context.Context, blocks []llm.ContentBlock) []llm.ContentBlock {
	var results []llm.ContentBlock
	for _, blk := range blocks {
		if blk.Type != llm.ContentTypeToolUse || blk.ToolUse == nil {
			continue
		}
		name := blk.ToolUse.Name
		args := blk.ToolUse.Input

		s.emit(Event{Kind: EventToolStart, ToolID: blk.ToolUse.ID, ToolName: name, ToolInput: args})

		content, isErr := s.runOneTool(ctx, name, args)

		s.emit(Event{Kind: EventToolEnd, ToolID: blk.ToolUse.ID, ToolName: name, Text: content, IsError: isErr})

		results = append(results, llm.ContentBlock{
			Type:       llm.ContentTypeToolResult,
			ToolResult: &llm.ToolResultBlock{ToolUseID: blk.ToolUse.ID, Content: content, IsError: isErr},
		})
	}
	return results
}

// runOneTool 审批 + 执行单个工具,返回 (内容, 是否错误)。
func (s *SubAgent) runOneTool(ctx context.Context, name string, args map[string]any) (string, bool) {
	argsJSON, err := json.Marshal(args)
	if err != nil {
		argsJSON = []byte("{}")
	}

	effective, denyMsg, blocked := hitl.ApplyGate(s.gate, s.guard, name, string(argsJSON))
	if blocked {
		return denyMsg, false // 被拒不是系统错误,作为普通结果回给 LLM
	}
	// 用户改了参数则解析回 map。
	if effective != string(argsJSON) {
		var modified map[string]any
		if err := json.Unmarshal([]byte(effective), &modified); err == nil {
			args = modified
		}
	}

	out, err := s.registry.ExecuteTool(ctx, name, args)
	if err != nil {
		return fmt.Sprintf("tool %s error: %v", name, err), true
	}
	// 工具成功后更新浏览器护栏会话状态(effective 即实际使用的参数)。
	if s.guard != nil {
		s.guard.AfterExecution(name, effective, out)
	}
	return out, false
}

func extractText(blocks []llm.ContentBlock) string {
	var b strings.Builder
	for _, blk := range blocks {
		if blk.Type == llm.ContentTypeText {
			b.WriteString(blk.Text)
		}
	}
	return strings.TrimSpace(b.String())
}
