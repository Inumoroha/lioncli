package memory

// 本文件定义 memory 包内部用的消息/对话类型,与具体 LLM provider 解耦。
// 把这些类型映射到 teacli 的 internal/llm 的适配器见 chat_adapter.go。

const defaultChatModel = "deepseek-chat"

// Message 是 memory 内部的消息表示:纯文本 Content + 可选工具调用。
// 字段对齐压缩/估算逻辑的需要(Role/Content/Name/ToolCallID/ToolCalls)。
type Message struct {
	Role       string
	Content    string
	Name       string
	ToolCallID string
	ToolCalls  []ToolCall
}

// ToolCall / ToolCallFunction 仅保留 token 估算与历史摘要需要的字段。
type ToolCall struct {
	Function ToolCallFunction
}

type ToolCallFunction struct {
	Name      string
	Arguments string
}

// Usage 是一次 LLM 调用的 token 用量。
type Usage struct {
	InputTokens  int
	OutputTokens int
}

// ChatResult 是 ChatClient.Chat 的返回:摘要正文 + 用量。
type ChatResult struct {
	Content string
	Usage   Usage
}

// ChatClient 是 memory 压缩/抽取事实时调用 LLM 的最小接口。
// 具体实现由 chat_adapter.go 适配 teacli 的 llm.Client。
type ChatClient interface {
	Chat(messages []Message) (ChatResult, error)
}

// SystemMessage / UserMessage / AssistantMessage 构造对应角色的消息。
func SystemMessage(content string) Message {
	return Message{Role: "system", Content: content}
}

func UserMessage(content string) Message {
	return Message{Role: "user", Content: content}
}

func AssistantMessage(content string) Message {
	return Message{Role: "assistant", Content: content}
}
