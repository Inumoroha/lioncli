package plan

import (
	"context"
	"errors"
	"strings"

	"lioncli/internal/llm"
)

// ChatClient 是 Planner 调用 LLM 的最小接口:给定 system+user,返回模型文本。
type ChatClient interface {
	Chat(system, user string) (string, error)
}

// LLMChatAdapter 把 teacli 的 llm.Client 适配成 plan.ChatClient。
type LLMChatAdapter struct {
	client llm.Client
	model  string
}

// NewLLMChatAdapter 用 teacli 的 llm.Client 构造适配器。model 为空回落默认。
func NewLLMChatAdapter(client llm.Client, model string) *LLMChatAdapter {
	if model == "" {
		model = defaultModel
	}
	return &LLMChatAdapter{client: client, model: model}
}

func (a *LLMChatAdapter) Chat(system, user string) (string, error) {
	if a == nil || a.client == nil {
		return "", errors.New("plan: llm client is nil")
	}

	resp, err := a.client.Chat(context.Background(), llm.ChatRequest{
		Model:  a.model,
		System: system,
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: user}}},
		},
	})
	if err != nil {
		return "", err
	}
	if resp == nil {
		return "", errors.New("plan: llm returned nil response")
	}

	var b strings.Builder
	for _, blk := range resp.Content {
		if blk.Type == llm.ContentTypeText {
			b.WriteString(blk.Text)
		}
	}
	return b.String(), nil
}
