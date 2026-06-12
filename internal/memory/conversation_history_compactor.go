package memory

import "strings"

type ConversationHistoryCompactor struct {
	chatClient         ChatClient
	retainRecentRounds int
}

func NewConversationHistoryCompactor(chatClient ChatClient, retainRecentRounds int) *ConversationHistoryCompactor {
	if retainRecentRounds <= 0 {
		retainRecentRounds = 3
	}
	return &ConversationHistoryCompactor{
		chatClient:         chatClient,
		retainRecentRounds: retainRecentRounds,
	}
}

func (c *ConversationHistoryCompactor) SetChatClient(chatClient ChatClient) {
	c.chatClient = chatClient
}

func (c *ConversationHistoryCompactor) CompactIfNeeded(history *[]Message, triggerTokens int) bool {
	if history == nil || len(*history) == 0 {
		return false
	}
	currentTokens := EstimateMessagesTokens(*history)
	if currentTokens < triggerTokens {
		return false
	}

	systemEnd := 0
	if (*history)[0].Role == "system" {
		systemEnd = 1
	}

	var userIndices []int
	for i := systemEnd; i < len(*history); i++ {
		if (*history)[i].Role == "user" {
			userIndices = append(userIndices, i)
		}
	}
	if len(userIndices) <= c.retainRecentRounds {
		return false
	}

	splitIdx := userIndices[len(userIndices)-c.retainRecentRounds]
	if splitIdx <= systemEnd {
		return false
	}

	oldMessages := append([]Message(nil), (*history)[systemEnd:splitIdx]...)
	if len(oldMessages) == 0 {
		return false
	}

	summary := c.Summarize(oldMessages)
	if strings.TrimSpace(summary) == "" {
		return false
	}

	var rebuilt []Message
	rebuilt = append(rebuilt, (*history)[:systemEnd]...)
	rebuilt = append(rebuilt, UserMessage("[compressed historical summary]\n"+strings.TrimSpace(summary)))
	rebuilt = append(rebuilt, AssistantMessage("Understood. I have the previous context. Please continue."))
	rebuilt = append(rebuilt, (*history)[splitIdx:]...)
	*history = rebuilt
	return true
}

// Summarize 把一段历史消息压成一句摘要文本(client 为 nil 时回退截断)。
// 导出供 agent 复用:agent 在 llm.Message 层做切分/拼装以保留工具块,
// 只借这里的摘要能力压旧前缀。
func (c *ConversationHistoryCompactor) Summarize(messages []Message) string {
	var builder strings.Builder
	for _, message := range messages {
		builder.WriteString(strings.ToUpper(message.Role))
		builder.WriteString(": ")
		builder.WriteString(message.Content)
		for _, toolCall := range message.ToolCalls {
			builder.WriteString("\n  TOOL_CALL ")
			builder.WriteString(toolCall.Function.Name)
			builder.WriteString(": ")
			builder.WriteString(toolCall.Function.Arguments)
		}
		builder.WriteString("\n\n")
		if builder.Len() > 60000 {
			builder.WriteString("...(truncated)\n")
			break
		}
	}
	if c.chatClient == nil {
		return fallbackSummary(builder.String(), 300)
	}
	response, err := c.chatClient.Chat([]Message{
		SystemMessage("Summarize the conversation history and keep goals, completed actions, conclusions, and unresolved issues."),
		UserMessage(builder.String()),
	})
	if err != nil || strings.TrimSpace(response.Content) == "" {
		return fallbackSummary(builder.String(), 300)
	}
	return response.Content
}
