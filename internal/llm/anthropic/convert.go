package anthropic

// 负责 内部类型 ↔ Anthropic 格式 的双向转换
import "lioncli/internal/llm"

func toAPI(req llm.ChatRequest) apiRequest {
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096 // Anthropic 要求必填，给个合理默认
	}
	return apiRequest{
		Model:     req.Model,
		System:    req.System,
		MaxTokens: maxTokens,
		Messages:  toAPIMessages(req.Messages),
		Tools:     toAPITools(req.Tools),
		Stream:    req.Stream,
	}
}

func toAPIMessages(msgs []llm.Message) []apiMessage {
	result := make([]apiMessage, 0, len(msgs))
	for _, m := range msgs {
		// Anthropic 没有独立的 system / tool role：
		// - system 在请求顶层字段处理，这里跳过
		// - tool 结果作为 user 消息发送（content 内含 tool_result 块）
		role := string(m.Role)
		switch m.Role {
		case llm.RoleSystem:
			continue
		case llm.RoleTool:
			role = "user"
		}
		result = append(result, apiMessage{
			Role:    role,
			Content: toAPIContent(m.Content),
		})
	}
	return result
}

func toAPIContent(blocks []llm.ContentBlock) []apiContent {
	result := make([]apiContent, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case llm.ContentTypeText:
			result = append(result, apiContent{
				Type: "text",
				Text: b.Text,
			})
		case llm.ContentTypeToolUse:
			if b.ToolUse == nil {
				continue
			}
			result = append(result, apiContent{
				Type:  "tool_use",
				ID:    b.ToolUse.ID,
				Name:  b.ToolUse.Name,
				Input: b.ToolUse.Input,
			})
		case llm.ContentTypeToolResult:
			if b.ToolResult == nil {
				continue
			}
			result = append(result, apiContent{
				Type:      "tool_result",
				ToolUseID: b.ToolResult.ToolUseID,
				Content:   b.ToolResult.Content,
				IsError:   b.ToolResult.IsError,
			})
		case llm.ContentTypeImage:
			if b.Image == nil {
				continue
			}
			result = append(result, apiContent{
				Type: "image",
				Source: &apiImageSource{
					Type:      "base64",
					MediaType: b.Image.MimeType,
					Data:      b.Image.Base64,
				},
			})
		}
	}
	return result
}

func toAPITools(tools []llm.Tool) []apiTool {
	result := make([]apiTool, 0, len(tools))
	for _, t := range tools {
		result = append(result, apiTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.Parameters,
		})
	}
	return result
}

// 反向：API 响应 → 内部类型
func fromAPIResponse(resp apiResponse) *llm.ChatResponse {
	return &llm.ChatResponse{
		ID:         resp.ID,
		Content:    fromAPIContent(resp.Content),
		StopReason: fromAPIStopReason(resp.StopReason),
		Usage: llm.Usage{
			InputTokens:  resp.Usage.InputTokens,
			OutputTokens: resp.Usage.OutputTokens,
		},
	}
}

func fromAPIContent(blocks []apiContent) []llm.ContentBlock {
	result := make([]llm.ContentBlock, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case "text":
			result = append(result, llm.ContentBlock{
				Type: llm.ContentTypeText,
				Text: b.Text,
			})
		case "tool_use":
			result = append(result, llm.ContentBlock{
				Type: llm.ContentTypeToolUse,
				ToolUse: &llm.ToolUseBlock{
					ID:    b.ID,
					Name:  b.Name,
					Input: b.Input,
				},
			})
		case "tool_result":
			result = append(result, llm.ContentBlock{
				Type: llm.ContentTypeToolResult,
				ToolResult: &llm.ToolResultBlock{
					ToolUseID: b.ToolUseID,
					Content:   b.Content,
					IsError:   b.IsError,
				},
			})
		}
	}
	return result
}

func fromAPIStopReason(reason string) llm.StopReason {
	switch reason {
	case "end_turn", "stop_sequence":
		return llm.StopReasonEndTurn
	case "tool_use":
		return llm.StopReasonToolUse
	case "max_tokens":
		return llm.StopReasonMaxTokens
	default:
		return llm.StopReasonEndTurn
	}
}
