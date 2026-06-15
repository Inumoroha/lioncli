package agent

// Agent 只面对 llm 和 tool 两层抽象：
// 它不知道工具背后是本地函数还是 MCP server。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"lioncli/internal/hitl"
	"lioncli/internal/image"
	"lioncli/internal/llm"
	"lioncli/internal/memory"
	"lioncli/internal/plan"
	"lioncli/internal/policy"
	"lioncli/internal/prompt"
	"lioncli/internal/rag"
	"lioncli/internal/skill"
	"lioncli/internal/tool"
)

// retainRecentRounds 是发送前压缩时保留的最近用户轮数;更早的前缀被摘要成一条消息。
const retainRecentRounds = 3

type Agent struct {
	llmClient llm.Client
	registry  *tool.ToolRegistry
	model     string
	history   []llm.Message
	runMu     sync.Mutex
	events    chan<- Event // 可选事件出口，由 TUI 等观察者通过 SetEvents 注入

	// 技能子系统:skillReg 提供系统提示里的技能索引;skillBuf 是与 load_skill
	// 工具共享的上下文缓冲,被加载的技能正文经它注入到下一条 user message。
	skillReg *skill.SkillRegistry
	skillBuf *skill.SkillContextBuffer

	// 系统提示词装配:assembler 把技能索引装进完整 system prompt(身份/语言/模式/
	// 审批/技能/上下文管理/收尾)。为 nil 时降级为只用技能索引那一段。
	assembler  *prompt.Assembler
	promptMode prompt.Mode

	// 记忆子系统:memMgr 记录对话/记 token 用量/召回长期记忆;compactor 在发送前
	// 把过长的 a.history 压缩(保留近期、摘要旧前缀)。memMgr 为 nil 时全部跳过。
	memMgr    *memory.MemoryManager
	compactor *memory.ConversationHistoryCompactor

	// 规划器:planner 把目标拆成 DAG 任务并按拓扑序执行(Plan 方法)。为 nil 时
	// `/plan` 命令返回"规划器未启用"。
	planner *plan.Planner

	// 代码检索:embedder 用于 /index 建立 RAG 索引(IndexProject 方法)。为 nil
	// 时 `/index` 返回"embedding 未配置"。
	embedder rag.Embedder

	// HITL 审批闸门:执行危险工具/MCP 工具前向用户征求批准。为 nil 或未启用时
	// 直接放行,行为与未接入审批时完全一致。由 TUI 等宿主通过 SetHitl 注入。
	gate hitl.HitlHandler

	// browserGuard:浏览器操作安全护栏(可选)。在审批前做硬拦截/敏感页单步审批,
	// 并在工具执行后更新浏览器会话状态。为 nil 时不影响任何工具。
	browserGuard hitl.ToolGuard
	audit        *policy.AuditLog
}

// SetHitl 注入 HITL 审批处理器。传 nil 表示关闭审批(危险工具直接执行)。
// 与 SetEvents 一样由宿主(TUI)在构建期注入;agent 只通过接口调用,不关心其实现。
func (a *Agent) SetHitl(h hitl.HitlHandler) {
	a.gate = h
}

// SetBrowserGuard 注入浏览器操作护栏(由 internal/browser.Guard 实现)。传 nil 关闭。
func (a *Agent) SetBrowserGuard(g hitl.ToolGuard) {
	a.browserGuard = g
}

func (a *Agent) SetAuditLog(log *policy.AuditLog) {
	a.audit = log
}

func (a *Agent) ToolSummaries() []string {
	if a == nil || a.registry == nil {
		return nil
	}
	tools := a.registry.GetAllTools()
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		desc := strings.TrimSpace(t.Description)
		if desc == "" {
			out = append(out, t.Name)
			continue
		}
		out = append(out, fmt.Sprintf("%s - %s", t.Name, desc))
	}
	return out
}

func New(
	client llm.Client,
	registry *tool.ToolRegistry,
	model string,
	skillReg *skill.SkillRegistry,
	skillBuf *skill.SkillContextBuffer,
	assembler *prompt.Assembler,
	memMgr *memory.MemoryManager,
	planner *plan.Planner,
	embedder rag.Embedder,
) *Agent {
	a := &Agent{
		llmClient:  client,
		registry:   registry,
		model:      model,
		skillReg:   skillReg,
		skillBuf:   skillBuf,
		assembler:  assembler,
		promptMode: prompt.ModeAgent, // TUI 目前只有执行模式
		memMgr:     memMgr,
		planner:    planner,
		embedder:   embedder,
		audit:      policy.DefaultAuditLog(),
	}
	if memMgr != nil {
		a.compactor = memMgr.NewHistoryCompactor(retainRecentRounds)
	}
	return a
}

func (a *Agent) Run(ctx context.Context, userInput string) (string, error) {
	a.runMu.Lock()
	defer a.runMu.Unlock()

	// 发送前先压缩既往历史(针对上一轮已闭合的对话,在 append 本轮输入之前)。
	a.compactHistoryIfNeeded()

	// 解析输入里的 @image:<path> / @clipboard 引用,构造(可能多模态的)user 消息。
	// 无引用时退回纯文本消息,行为与未接入图片时完全一致。baseDir 用当前工作目录解析相对路径。
	baseDir, _ := os.Getwd()
	a.history = append(a.history, image.UserMessage(userInput, baseDir))
	if a.memMgr != nil {
		// 记忆/token 估算保持纯文本(图片块对记忆无意义),仍记原始输入文本。
		a.memMgr.AddUserMessage(userInput)
	}

	// 系统提示词:技能集在一次 Run 内不变,装配一次即可。
	// 先算技能索引(skillReg 为 nil 时为空),再交给 assembler 装进完整 system
	// prompt;assembler 为 nil 或装配失败时降级为只用技能索引那一段,不阻断对话。
	var skillIndex string
	if a.skillReg != nil {
		skillIndex = skill.FormatSkillIndex(a.skillReg.EnabledSkills())
	}
	// 召回与本轮输入相关的长期记忆,作为 ## Project Context 段(为空时 assembler 自动省略)。
	var memCtx string
	if a.memMgr != nil {
		memCtx = a.memMgr.BuildContextForQuery(userInput, 1500)
	}
	systemPrompt := skillIndex
	if a.assembler != nil {
		assembled, err := a.assembler.Assemble(a.promptMode, prompt.Context{
			SkillIndex:    skillIndex,
			MemoryContext: memCtx,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "warn: 装配 system prompt 失败,降级为仅技能索引: %v\n", err)
		} else {
			systemPrompt = assembled
		}
	}

	for {
		tools := a.registry.ToLLMTools()

		resp, err := a.llmClient.Chat(ctx, llm.ChatRequest{
			Model:    a.model,
			System:   systemPrompt,
			Messages: a.history,
			Tools:    tools,
		})
		if err != nil {
			a.emit(Event{Kind: EventError, Err: err})
			return "", err
		}

		a.history = append(a.history, llm.Message{
			Role:    llm.RoleAssistant,
			Content: resp.Content,
		})

		// 记 token 用量 + 把 assistant 文本记进短期记忆(memMgr 内部超预算会自动压缩)。
		if a.memMgr != nil {
			a.memMgr.RecordTokenUsage(resp.Usage.InputTokens, resp.Usage.OutputTokens)
			if text := extractText(resp.Content); strings.TrimSpace(text) != "" {
				a.memMgr.AddAssistantMessage(text)
			}
		}

		// 实时把本轮 assistant 的文本块推给观察者。
		// 同一轮内可能既有 text 又有 tool_use（DeepSeek/Claude 都允许），
		// 这里逐块推，TUI 顺序追加渲染。
		for _, blk := range resp.Content {
			if blk.Type == llm.ContentTypeText && blk.Text != "" {
				a.emit(Event{Kind: EventAssistantText, Text: blk.Text})
			}
		}

		if resp.StopReason != llm.StopReasonToolUse {
			return extractText(resp.Content), nil
		}

		toolResults := a.executeTools(ctx, resp.Content)
		// 用 RoleTool 让 provider 自己挑协议形态：
		// - openai/deepseek 拆成多条 role:"tool" 消息
		// - anthropic 转成 user 消息里嵌 tool_result 块
		a.history = append(a.history, llm.Message{
			Role:    llm.RoleTool,
			Content: toolResults,
		})

		// load_skill 把技能正文 Push 进 skillBuf;在下一轮 Chat 前把它 Drain
		// 出来、作为一条 user message 注入,使技能正文进入对话上下文。
		// 消息序列:assistant(tool_use) → tool(ack) → user(## 已加载 Skill …)。
		if a.skillBuf != nil && !a.skillBuf.IsEmpty() {
			if loaded := a.skillBuf.Drain(); loaded != "" {
				a.history = append(a.history, llm.Message{
					Role:    llm.RoleUser,
					Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: loaded}},
				})
			}
		}
	}
}

func extractText(blocks []llm.ContentBlock) string {
	var b strings.Builder
	for _, blk := range blocks {
		if blk.Type == llm.ContentTypeText {
			b.WriteString(blk.Text)
		}
	}
	return b.String()
}

func (a *Agent) executeTools(ctx context.Context, blocks []llm.ContentBlock) []llm.ContentBlock {
	var results []llm.ContentBlock
	for _, blk := range blocks {
		if blk.Type != llm.ContentTypeToolUse || blk.ToolUse == nil {
			continue
		}

		name := blk.ToolUse.Name
		args := blk.ToolUse.Input
		start := time.Now()
		argsJSON := toolArgsJSON(args)

		a.emit(Event{
			Kind:      EventToolStart,
			ToolID:    blk.ToolUse.ID,
			ToolName:  name,
			ToolInput: args,
		})

		// HITL 审批闸门:危险工具/MCP 工具在落地前先征求用户同意。
		// 被拒/跳过时不执行,直接把一条说明回给 LLM(非 error,LLM 可据此另作打算);
		// 用户改了参数则用新参数执行。闸门未接入或未启用时此调用零开销放行。
		if msg, blocked := a.approveTool(name, &args); blocked {
			a.recordAudit(policy.DenyByHITL(name, argsJSON, msg, time.Since(start)))
			result := &llm.ToolResultBlock{ToolUseID: blk.ToolUse.ID, Content: msg}
			a.emit(Event{
				Kind:       EventToolEnd,
				ToolID:     blk.ToolUse.ID,
				ToolName:   name,
				ToolOutput: msg,
			})
			if a.memMgr != nil {
				a.memMgr.AddToolResult(name, msg)
			}
			results = append(results, llm.ContentBlock{
				Type:       llm.ContentTypeToolResult,
				ToolResult: result,
			})
			continue
		}

		argsJSON = toolArgsJSON(args)
		out, err := a.registry.ExecuteTool(ctx, name, args)
		result := &llm.ToolResultBlock{ToolUseID: blk.ToolUse.ID}
		if err != nil {
			result.Content = fmt.Sprintf("tool %s error: %v", name, err)
			result.IsError = true
			var policyErr policy.Error
			if errors.As(err, &policyErr) {
				a.recordAudit(policy.DenyByPolicy(name, argsJSON, err.Error(), time.Since(start)))
			} else {
				a.recordAudit(policy.ToolError(name, argsJSON, err.Error(), time.Since(start)))
			}
		} else {
			result.Content = out
			a.recordAudit(policy.Allow(name, argsJSON, time.Since(start)))
			// 工具成功后让浏览器护栏更新会话状态(记导航 URL / 新开标签页)。
			a.afterToolExecution(name, args, out)
		}

		a.emit(Event{
			Kind:       EventToolEnd,
			ToolID:     blk.ToolUse.ID,
			ToolName:   blk.ToolUse.Name,
			ToolOutput: result.Content,
			IsError:    result.IsError,
		})

		if a.memMgr != nil {
			a.memMgr.AddToolResult(blk.ToolUse.Name, result.Content)
		}

		results = append(results, llm.ContentBlock{
			Type:       llm.ContentTypeToolResult,
			ToolResult: result,
		})
	}
	return results
}

func (a *Agent) recordAudit(entry policy.AuditEntry) {
	if a.audit != nil {
		a.audit.Record(entry)
	}
}

func toolArgsJSON(args map[string]any) string {
	raw, err := json.Marshal(args)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

// compactHistoryIfNeeded 在发送前压缩过长的 a.history:保留最近 retainRecentRounds
// 轮(以 user 消息为界),更早的前缀摘要成一条 user 消息 + 一条 assistant 占位。
// 近期消息保持原始 llm.Message(工具块完整),只把旧前缀转文本喂摘要,保证工具配对不被打散。
func (a *Agent) compactHistoryIfNeeded() {
	if a.memMgr == nil || a.compactor == nil {
		return
	}

	triggerTokens := int(0.9 * float64(a.memMgr.TokenBudget().AvailableForConversation()))
	if memory.EstimateMessagesTokens(toMemoryMessages(a.history)) < triggerTokens {
		return
	}

	// teacli 历史里不含 system 消息(System 是 ChatRequest 字段),故按 user 边界切分即可。
	var userIdx []int
	for i, m := range a.history {
		if m.Role == llm.RoleUser {
			userIdx = append(userIdx, i)
		}
	}
	if len(userIdx) <= retainRecentRounds {
		return
	}
	split := userIdx[len(userIdx)-retainRecentRounds]
	if split <= 0 {
		return
	}

	summary := a.compactor.Summarize(toMemoryMessages(a.history[:split]))
	if strings.TrimSpace(summary) == "" {
		return
	}

	compacted := make([]llm.Message, 0, 2+len(a.history)-split)
	compacted = append(compacted, textMessage(llm.RoleUser, "[compressed historical summary]\n"+strings.TrimSpace(summary)))
	compacted = append(compacted, textMessage(llm.RoleAssistant, "Understood. I have the previous context. Please continue."))
	compacted = append(compacted, a.history[split:]...) // 近期原样保留,工具块完整
	a.history = compacted
}

// toMemoryMessages 把 llm.Message 转成 memory.Message,仅用于 token 估算与摘要(有损可接受):
// 文本块并进 Content,tool_use 取工具名进 ToolCalls,tool_result 内容并进 Content。
func toMemoryMessages(msgs []llm.Message) []memory.Message {
	out := make([]memory.Message, 0, len(msgs))
	for _, m := range msgs {
		mm := memory.Message{Role: string(m.Role)}
		var text strings.Builder
		for _, blk := range m.Content {
			switch blk.Type {
			case llm.ContentTypeText:
				text.WriteString(blk.Text)
			case llm.ContentTypeToolUse:
				if blk.ToolUse != nil {
					mm.ToolCalls = append(mm.ToolCalls, memory.ToolCall{
						Function: memory.ToolCallFunction{Name: blk.ToolUse.Name},
					})
				}
			case llm.ContentTypeToolResult:
				if blk.ToolResult != nil {
					if text.Len() > 0 {
						text.WriteString("\n")
					}
					text.WriteString(blk.ToolResult.Content)
				}
			}
		}
		mm.Content = text.String()
		out = append(out, mm)
	}
	return out
}

// textMessage 包一条纯文本 llm.Message。
func textMessage(role llm.Role, text string) llm.Message {
	return llm.Message{
		Role:    role,
		Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: text}},
	}
}
