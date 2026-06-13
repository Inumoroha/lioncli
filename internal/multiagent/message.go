package multiagent

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// AgentMessageType 表示 Agent 间通信消息的类型。
type AgentMessageType string

const (
	AgentMessageTypeTask      AgentMessageType = "TASK"
	AgentMessageTypeResult    AgentMessageType = "RESULT"
	AgentMessageTypeFeedback  AgentMessageType = "FEEDBACK"
	AgentMessageTypeApproval  AgentMessageType = "APPROVAL"
	AgentMessageTypeRejection AgentMessageType = "REJECTION"
	AgentMessageTypeError     AgentMessageType = "ERROR"
)

var validMessageTypes = map[AgentMessageType]struct{}{
	AgentMessageTypeTask:      {},
	AgentMessageTypeResult:    {},
	AgentMessageTypeFeedback:  {},
	AgentMessageTypeApproval:  {},
	AgentMessageTypeRejection: {},
	AgentMessageTypeError:     {},
}

func (t AgentMessageType) IsValid() bool {
	_, ok := validMessageTypes[t]
	return ok
}

// AgentMessage 是 Multi-Agent 协作中的基础通信单元。
//
// 设计说明:
//  1. 核心字段:fromAgent、fromRole、content、type。
//  2. ID、会话、时间戳、接收方、元数据为可选扩展字段,便于后续追踪和路由。
//  3. Content 用 string:当前模型以自然语言通信为主,直接用 string 便于构造与调试。
type AgentMessage struct {
	ID        string            `json:"id,omitempty"`
	SessionID string            `json:"sessionId,omitempty"`
	CreatedAt time.Time         `json:"createdAt,omitempty"`
	FromAgent string            `json:"fromAgent"`
	FromRole  AgentRole         `json:"fromRole,omitempty"`
	ToAgent   string            `json:"toAgent,omitempty"`
	ToRole    AgentRole         `json:"toRole,omitempty"`
	Type      AgentMessageType  `json:"type"`
	Content   string            `json:"content"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// NewAgentMessage 创建一条基础消息,并自动补齐追踪字段。
func NewAgentMessage(fromAgent string, fromRole AgentRole, messageType AgentMessageType, content string) *AgentMessage {
	return &AgentMessage{
		ID:        uuid.NewString(),
		CreatedAt: time.Now(),
		FromAgent: strings.TrimSpace(fromAgent),
		FromRole:  fromRole,
		Type:      messageType,
		Content:   strings.TrimSpace(content),
	}
}

// TaskMessage 创建任务消息(主控 -> 子代理)。
func TaskMessage(fromAgent, content string) *AgentMessage {
	return NewAgentMessage(fromAgent, "", AgentMessageTypeTask, content)
}

// ResultMessage 创建结果消息(子代理 -> 主控)。
func ResultMessage(fromAgent string, role AgentRole, content string) *AgentMessage {
	return NewAgentMessage(fromAgent, role, AgentMessageTypeResult, content)
}

// FeedbackMessage 创建反馈消息(检查者 -> 主控)。
func FeedbackMessage(fromAgent, content string) *AgentMessage {
	return NewAgentMessage(fromAgent, AgentRoleReviewer, AgentMessageTypeFeedback, content)
}

// ApprovalMessage 创建审批通过消息。
func ApprovalMessage(fromAgent, content string) *AgentMessage {
	return NewAgentMessage(fromAgent, AgentRoleReviewer, AgentMessageTypeApproval, content)
}

// RejectionMessage 创建拒绝消息。
func RejectionMessage(fromAgent, content string) *AgentMessage {
	return NewAgentMessage(fromAgent, AgentRoleReviewer, AgentMessageTypeRejection, content)
}

// ErrorMessage 创建错误消息(子代理执行过程中出现系统级错误)。
func ErrorMessage(fromAgent string, role AgentRole, content string) *AgentMessage {
	return NewAgentMessage(fromAgent, role, AgentMessageTypeError, content)
}

// WithSession 设置会话 ID,便于串联同一轮协作中的消息。
func (m *AgentMessage) WithSession(sessionID string) *AgentMessage {
	m.SessionID = strings.TrimSpace(sessionID)
	return m
}

// WithRecipient 设置接收方 Agent 或角色。
func (m *AgentMessage) WithRecipient(toAgent string, toRole AgentRole) *AgentMessage {
	m.ToAgent = strings.TrimSpace(toAgent)
	m.ToRole = toRole
	return m
}

// WithMetadata 设置附加元数据。
func (m *AgentMessage) WithMetadata(metadata map[string]string) *AgentMessage {
	if len(metadata) == 0 {
		m.Metadata = nil
		return m
	}
	cloned := make(map[string]string, len(metadata))
	for k, v := range metadata {
		cloned[k] = v
	}
	m.Metadata = cloned
	return m
}

// Validate 校验消息是否满足最小发送条件。
func (m AgentMessage) Validate() error {
	if strings.TrimSpace(m.FromAgent) == "" {
		return fmt.Errorf("fromAgent 不能为空")
	}
	if !m.Type.IsValid() {
		return fmt.Errorf("非法消息类型: %q", m.Type)
	}
	if strings.TrimSpace(m.Content) == "" {
		return fmt.Errorf("content 不能为空")
	}
	if m.FromRole != "" && !m.FromRole.IsValid() {
		return fmt.Errorf("非法发送方角色: %q", m.FromRole)
	}
	if m.ToRole != "" && !m.ToRole.IsValid() {
		return fmt.Errorf("非法接收方角色: %q", m.ToRole)
	}
	return nil
}
