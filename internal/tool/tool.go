package tool

import "context"

// 每个工具的通用函数签名。每个工具都有一个执行函数，用于执行工具的逻辑。执行函数的参数是一个上下文，用于传递执行时的上下文信息。执行函数的返回值是一个字符串，用于存储工具的执行结果。执行函数的返回值是一个错误，用于存储工具执行时的错误信息。
type Executor func(ctx context.Context, args map[string]any) (string, error)

// 翻译: 描述了一个可调用的工具。LLM提供程序只接收Name、Description和Parameters。
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
	Execute     Executor       `json:"-"`
}
