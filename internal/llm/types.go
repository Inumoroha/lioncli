package llm

// Message —— 内部统一的消息表示
type Message struct {
	Role    Role
	Content []ContentBlock  // 支持多模态：文本/图片/工具调用
}

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
	RoleTool      Role = "tool"
)

// ContentBlock —— 一条消息可以包含多个块
type ContentBlock struct {
	Type ContentType

	// 不同类型用不同字段（or 用 interface）
	Text       string         	// Type == Text
	ToolUse    *ToolUseBlock  	// Type == ToolUse
	ToolResult *ToolResultBlock // Type == ToolResult
	Image      *ImageBlock    	// Type == Image
}

type ContentType string

const (
	ContentTypeText       ContentType = "text"
	ContentTypeToolUse    ContentType = "tool_use"
	ContentTypeToolResult ContentType = "tool_result"
	ContentTypeImage      ContentType = "image"
)

// ImageBlock —— 多模态图片块：base64 编码的图片字节 + MIME 类型（如 image/png）。
// provider 各自把它翻成自己的协议形态（OpenAI 的 data URI / Anthropic 的 base64 source）。
type ImageBlock struct {
	Base64   string
	MimeType string
}

func ImageContent(base64, mimeType string) ContentBlock {
	return ContentBlock{
		Type:  ContentTypeImage,
		Image: &ImageBlock{Base64: base64, MimeType: mimeType},
	}
}

type ToolUseBlock struct {
	ID    string
	Name  string
	Input map[string]any  // 已解析的对象（OpenAI 的字符串在这里也解析掉）
}

type ToolResultBlock struct {
	ToolUseID string
	Content   string
	IsError   bool
}

// Tool 统一的工具描述（不绑定任何 provider）
type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"` // 工具参数，具体结构由工具定义
}

// ChatRequest —— 统一的请求
  type ChatRequest struct {
      Model       string
      System      string
      Messages    []Message
      Tools       []Tool
      MaxTokens   int
      Temperature float64
      Stream      bool
  }

// ChatResponse —— 统一的响应
type ChatResponse struct {
	ID         string
	Content    []ContentBlock  // 可能有文本 + 工具调用
	StopReason StopReason
	Usage      Usage
}

type StopReason string

const (
	StopReasonEndTurn   StopReason = "end_turn"
	StopReasonToolUse   StopReason = "tool_use"
	StopReasonMaxTokens StopReason = "max_tokens"
)

type Usage struct {
	InputTokens  int
	OutputTokens int
}