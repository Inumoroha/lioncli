package openai

// 这些是 OpenAI Chat Completions API 真实的 JSON 格式。
// 全部小写开头（unexported），仅在 openai 包内可见；外部统一用 llm.* 类型。

type apiRequest struct {
	Model       string       `json:"model"`
	Messages    []apiMessage `json:"messages"`
	Tools       []apiTool    `json:"tools,omitempty"`
	MaxTokens   int          `json:"max_tokens,omitempty"`
	Temperature *float64     `json:"temperature,omitempty"`
	Stream      bool         `json:"stream,omitempty"`
}

// apiMessage 同时承担请求里的输入消息和响应里的 choices[].message。
// OpenAI 协议里：
//   - role=system/user：content 是字符串
//   - role=assistant：content 可能为空，配合 tool_calls
//   - role=tool：content 是工具结果字符串，tool_call_id 关联调用
// Content 是 any：纯文本消息序列化成 JSON 字符串（与单模态时完全一致），
// 含图片的 user 消息序列化成 []apiContentPart 数组（OpenAI 多模态格式）。
// 响应里 content 是字符串/null，fromAPIMessage 用类型断言取回文本。
type apiMessage struct {
	Role       string        `json:"role"`
	Content    any           `json:"content,omitempty"`
	Name       string        `json:"name,omitempty"`
	ToolCalls  []apiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
}

// apiContentPart —— OpenAI 多模态 content 数组里的一个部分（文本或图片）。
type apiContentPart struct {
	Type     string       `json:"type"` // "text" 或 "image_url"
	Text     string       `json:"text,omitempty"`
	ImageURL *apiImageURL `json:"image_url,omitempty"`
}

type apiImageURL struct {
	URL string `json:"url"` // data URI: data:<mime>;base64,<data>
}

type apiToolCall struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"` // 固定 "function"
	Function apiFunctionCall `json:"function"`
}

type apiFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON 字符串
}

type apiTool struct {
	Type     string         `json:"type"` // 固定 "function"
	Function apiFunctionDef `json:"function"`
}

type apiFunctionDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type apiResponse struct {
	ID      string      `json:"id"`
	Object  string      `json:"object"`
	Model   string      `json:"model"`
	Choices []apiChoice `json:"choices"`
	Usage   apiUsage    `json:"usage"`
}

type apiChoice struct {
	Index        int        `json:"index"`
	Message      apiMessage `json:"message"`
	FinishReason string     `json:"finish_reason"`
}

type apiUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type apiError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Code    string `json:"code"`
}

type apiErrorResponse struct {
	Error apiError `json:"error"`
}
