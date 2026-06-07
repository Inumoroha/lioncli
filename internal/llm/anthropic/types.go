package anthropic

// 这些是 Anthropic API 真实的 JSON 格式
// 注意全用小写开头（unexported），这些类型只在 anthropic 包内可见，外部用 llm.Tool 等统一类型。

type apiRequest struct {
	Model     string       `json:"model"`
	Messages  []apiMessage `json:"messages"`
	System    string       `json:"system,omitempty"`
	MaxTokens int          `json:"max_tokens"`
	Tools     []apiTool    `json:"tools,omitempty"`
	Stream    bool         `json:"stream,omitempty"`
}

type apiMessage struct {
	Role    string       `json:"role"`
	Content []apiContent `json:"content"`
}

type apiContent struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     map[string]any  `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
	Source    *apiImageSource `json:"source,omitempty"` // Type == "image"
}

// apiImageSource —— Anthropic 图片块的 base64 载荷。
type apiImageSource struct {
	Type      string `json:"type"`       // 固定 "base64"
	MediaType string `json:"media_type"` // 如 image/png
	Data      string `json:"data"`       // base64 字节
}

type apiTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

type apiResponse struct {
	ID         string       `json:"id"`
	Type       string       `json:"type"`
	Role       string       `json:"role"`
	Model      string       `json:"model"`
	Content    []apiContent `json:"content"`
	StopReason string       `json:"stop_reason"`
	Usage      apiUsage     `json:"usage"`
}

type apiUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type apiError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type apiErrorResponse struct {
	Type  string   `json:"type"`
	Error apiError `json:"error"`
}
