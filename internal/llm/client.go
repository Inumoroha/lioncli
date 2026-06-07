package llm

import "context"

// Client —— 所有 LLM provider 都实现这个接口
type Client interface {
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
	// 后续可加 Stream(ctx, req) (<-chan StreamEvent, error)
}

// defaultAskMaxTokens 是 Ask 单轮问答的输出上限，Anthropic 要求必填。
const defaultAskMaxTokens = 4096

// Ask 简化接口：单轮文本问答，返回响应里的第一个文本块。
// 它对所有 provider 通用，故作为包级函数而非接口方法，避免每个 provider 重复实现。
func Ask(ctx context.Context, c Client, question string) (string, error) {
	resp, err := c.Chat(ctx, ChatRequest{
		Messages: []Message{
			{
				Role:    RoleUser,
				Content: []ContentBlock{{Type: ContentTypeText, Text: question}},
			},
		},
		MaxTokens: defaultAskMaxTokens,
	})
	if err != nil {
		return "", err
	}
	for _, b := range resp.Content {
		if b.Type == ContentTypeText {
			return b.Text, nil
		}
	}
	return "", nil
}