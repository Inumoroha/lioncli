package openai

// 负责 内部类型 ↔ OpenAI 格式 的双向转换
import (
	"encoding/json"
	"lioncli/internal/llm"
	"strings"
)

func toAPI(req llm.ChatRequest) apiRequest {
	out := apiRequest{
		Model:     req.Model,
		Messages:  toAPIMessages(req.System, req.Messages),
		Tools:     toAPITools(req.Tools),
		MaxTokens: req.MaxTokens,
		Stream:    req.Stream,
	}
	if req.Temperature != 0 {
		t := req.Temperature
		out.Temperature = &t
	}
	return out
}

func toAPIMessages(system string, msgs []llm.Message) []apiMessage {
	out := make([]apiMessage, 0, len(msgs)+1)
	if strings.TrimSpace(system) != "" {
		out = append(out, apiMessage{Role: "system", Content: system})
	}
	for _, m := range msgs {
		switch m.Role {
		case llm.RoleSystem:
			out = append(out, apiMessage{
				Role:    "system",
				Content: collectText(m.Content),
			})
		case llm.RoleUser:
			out = append(out, toUserMessage(m.Content))
		case llm.RoleAssistant:
			out = append(out, toAssistantMessage(m.Content))
		case llm.RoleTool:
			// 一条内部 tool 消息可能包含多个 tool_result，OpenAI 要求每个独立成一条
			out = append(out, toToolMessages(m.Content)...)
		}
	}
	return out
}

// collectText 合并所有文本块的文本内容，返回一个字符串。
func collectText(blocks []llm.ContentBlock) string {
	var sb strings.Builder
	for _, b := range blocks {
		if b.Type == llm.ContentTypeText {
			sb.WriteString(b.Text)
		}
	}
	return sb.String()
}

// toUserMessage 构造 user 消息：无图片块时 content 用纯字符串（与单模态完全一致），
// 含图片块时 content 用 []apiContentPart 数组（文本部分 + 每张图一个 image_url data URI）。
func toUserMessage(blocks []llm.ContentBlock) apiMessage {
	hasImage := false
	for _, b := range blocks {
		if b.Type == llm.ContentTypeImage && b.Image != nil {
			hasImage = true
			break
		}
	}
	if !hasImage {
		return apiMessage{Role: "user", Content: collectText(blocks)}
	}

	parts := make([]apiContentPart, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case llm.ContentTypeText:
			if b.Text == "" {
				continue
			}
			parts = append(parts, apiContentPart{Type: "text", Text: b.Text})
		case llm.ContentTypeImage:
			if b.Image == nil {
				continue
			}
			parts = append(parts, apiContentPart{
				Type:     "image_url",
				ImageURL: &apiImageURL{URL: "data:" + b.Image.MimeType + ";base64," + b.Image.Base64},
			})
		}
	}
	return apiMessage{Role: "user", Content: parts}
}

func toAssistantMessage(blocks []llm.ContentBlock) apiMessage {
	msg := apiMessage{Role: "assistant"}
	var sb strings.Builder
	for _, b := range blocks {
		switch b.Type {
		case llm.ContentTypeText:
			sb.WriteString(b.Text)
		case llm.ContentTypeToolUse:
			if b.ToolUse == nil {
				continue
			}
			args, _ := json.Marshal(b.ToolUse.Input)
			msg.ToolCalls = append(msg.ToolCalls, apiToolCall{
				ID:   b.ToolUse.ID,
				Type: "function",
				Function: apiFunctionCall{
					Name:      b.ToolUse.Name,
					Arguments: string(args),
				},
			})
		}
	}
	// Content 现为 any:空文本时留 nil,靠 omitempty 省略(保持改造前的序列化形态);
	// 非空才写字符串。assistant 带 tool_calls、content 为空是合法的。
	if text := sb.String(); text != "" {
		msg.Content = text
	}
	return msg
}

func toToolMessages(blocks []llm.ContentBlock) []apiMessage {
	result := make([]apiMessage, 0, len(blocks))
	for _, b := range blocks {
		if b.Type != llm.ContentTypeToolResult || b.ToolResult == nil {
			continue
		}
		content := b.ToolResult.Content
		if b.ToolResult.IsError && content == "" {
			content = "error"
		}
		result = append(result, apiMessage{
			Role:       "tool",
			ToolCallID: b.ToolResult.ToolUseID,
			Content:    content,
		})
	}
	return result
}

func toAPITools(tools []llm.Tool) []apiTool {
	result := make([]apiTool, 0, len(tools))
	for _, t := range tools {
		result = append(result, apiTool{
			Type: "function",
			Function: apiFunctionDef{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}
	return result
}

// 反向：API 响应 → 内部类型
func fromAPIResponse(resp apiResponse) *llm.ChatResponse {
	out := &llm.ChatResponse{
		ID: resp.ID,
		Usage: llm.Usage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
		},
	}
	if len(resp.Choices) == 0 {
		return out
	}
	choice := resp.Choices[0]
	out.Content = fromAPIMessage(choice.Message)
	out.StopReason = fromAPIFinishReason(choice.FinishReason)
	return out
}

func fromAPIMessage(msg apiMessage) []llm.ContentBlock {
	result := make([]llm.ContentBlock, 0, 1+len(msg.ToolCalls))
	// 响应里 content 是 JSON 字符串或 null：断言取文本，非字符串（理论上不会出现）忽略。
	if text, ok := msg.Content.(string); ok && text != "" {
		result = append(result, llm.ContentBlock{
			Type: llm.ContentTypeText,
			Text: text,
		})
	}
	for _, tc := range msg.ToolCalls {
		input := map[string]any{}
		if tc.Function.Arguments != "" {
			// 服务端给的是 JSON 字符串，解析失败时保留原文以便上游决策
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &input); err != nil {
				input = map[string]any{"_raw": tc.Function.Arguments}
			}
		}
		result = append(result, llm.ContentBlock{
			Type: llm.ContentTypeToolUse,
			ToolUse: &llm.ToolUseBlock{
				ID:    tc.ID,
				Name:  tc.Function.Name,
				Input: input,
			},
		})
	}
	return result
}

func fromAPIFinishReason(reason string) llm.StopReason {
	switch reason {
	case "stop":
		return llm.StopReasonEndTurn
	case "tool_calls", "function_call":
		return llm.StopReasonToolUse
	case "length":
		return llm.StopReasonMaxTokens
	default:
		return llm.StopReasonEndTurn
	}
}
