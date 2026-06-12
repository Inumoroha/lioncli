package memory

import (
	"context"
	"errors"
	"strings"

	"lioncli/internal/llm"
)

// LLMChatAdapter 把 teacli 的 llm.Client 适配成 memory 的 ChatClient,
// 供 ContextCompressor / ConversationHistoryCompactor 调 LLM 做摘要、抽事实。
//
// 两处转换:
//   - memory.Message 的 system 角色 → 汇集到 teacli ChatRequest.System;
//     其余角色 → llm.Message(纯文本 ContentBlock)。
//   - teacli 的 resp.Content([]ContentBlock)抽出文本 → ChatResult.Content。
type LLMChatAdapter struct {
	client      llm.Client
	model       string
	temperature float64
	maxTokens   int
}

// NewLLMChatAdapter 用 lioncli 的 llm.Client 构造适配器。model 为空回落默认。
//
// 参数：
//   client - llm.Client 接口，用于调用 LLM 进行对话
//   model  - 模型名称，为空时使用默认模型 defaultChatModel
//
// 返回值：
//   *LLMChatAdapter - 配置好的适配器实例，默认 temperature=0.2，maxTokens=512
func NewLLMChatAdapter(client llm.Client, model string) *LLMChatAdapter {
	// 模型名称为空时使用默认模型
	if model == "" {
		model = defaultChatModel
	}
	// 创建并返回适配器实例，设置默认参数
	return &LLMChatAdapter{
		client:      client,
		model:       model,
		temperature: 0.2, // 默认低温度，输出更确定性
		maxTokens:   512, // 默认最大令牌数
	}
}

func (a *LLMChatAdapter) WithTemperature(temperature float64) *LLMChatAdapter {
	a.temperature = temperature
	return a
}

func (a *LLMChatAdapter) WithMaxTokens(maxTokens int) *LLMChatAdapter {
	a.maxTokens = maxTokens
	return a
}

func (a *LLMChatAdapter) Chat(messages []Message) (ChatResult, error) {
	if a == nil || a.client == nil {
		return ChatResult{}, errors.New("llm client is nil")
	}

	var system strings.Builder
	msgs := make([]llm.Message, 0, len(messages))
	for _, m := range messages {
		if m.Role == "system" {
			if system.Len() > 0 {
				system.WriteString("\n\n")
			}
			system.WriteString(m.Content)
			continue
		}
		msgs = append(msgs, llm.Message{
			Role:    toLLMRole(m.Role),
			Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: m.Content}},
		})
	}

	resp, err := a.client.Chat(context.Background(), llm.ChatRequest{
		Model:       a.model,
		System:      system.String(),
		Messages:    msgs,
		Temperature: a.temperature,
		MaxTokens:   a.maxTokens,
	})
	if err != nil {
		return ChatResult{}, err
	}
	if resp == nil {
		return ChatResult{}, errors.New("llm returned nil response")
	}

	return ChatResult{
		Content: extractText(resp.Content),
		Usage: Usage{
			InputTokens:  resp.Usage.InputTokens,
			OutputTokens: resp.Usage.OutputTokens,
		},
	}, nil
}

func toLLMRole(role string) llm.Role {
	switch role {
	case "assistant":
		return llm.RoleAssistant
	case "system":
		return llm.RoleSystem
	case "tool":
		return llm.RoleTool
	default:
		return llm.RoleUser
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