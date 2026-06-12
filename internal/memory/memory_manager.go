package memory

import "fmt"

const maxToolResultChars = 500

type MemoryManager struct {
	shortTermMemory *ConversationMemory
	longTermMemory  *LongTermMemory
	compressor      *ContextCompressor
	retriever       *MemoryRetriever
	tokenBudget     *TokenBudget
	contextProfile  ContextProfile
	chatClient      ChatClient
}

func NewMemoryManager(chatClient ChatClient) (*MemoryManager, error) {
	return NewMemoryManagerWithProfile(chatClient, DefaultContextProfile(), "")
}

func NewMemoryManagerWithBudget(chatClient ChatClient, shortTermBudget, contextWindow int, storageDir string) (*MemoryManager, error) {
	return NewMemoryManagerWithProfile(chatClient, CustomContextProfile(contextWindow, shortTermBudget), storageDir)
}

func NewMemoryManagerWithProfile(chatClient ChatClient, profile ContextProfile, storageDir string) (*MemoryManager, error) {
	longTerm, err := NewLongTermMemory(storageDir)
	if err != nil {
		return nil, err
	}
	shortTerm := NewConversationMemory(profile.ShortTermMemoryBudget)
	manager := &MemoryManager{
		shortTermMemory: shortTerm,
		longTermMemory:  longTerm,
		compressor:      NewContextCompressor(chatClient, 3),
		retriever:       NewMemoryRetriever(shortTerm, longTerm),
		tokenBudget:     NewTokenBudget(profile.MaxContextWindow),
		contextProfile:  profile,
		chatClient:      chatClient,
	}
	return manager, nil
}

// NewHistoryCompactor 用本 manager 的 chatClient 造一个对话历史压缩器,
// 供 agent 在发送前压缩 a.history(保留最近 retainRounds 轮,旧前缀摘要)。
func (m *MemoryManager) NewHistoryCompactor(retainRounds int) *ConversationHistoryCompactor {
	return NewConversationHistoryCompactor(m.chatClient, retainRounds)
}

func (m *MemoryManager) SetChatClient(chatClient ChatClient) {
	m.chatClient = chatClient
	m.compressor.SetChatClient(chatClient)
}

func (m *MemoryManager) ApplyContextProfile(profile ContextProfile) {
	m.contextProfile = profile
	m.tokenBudget = NewTokenBudget(profile.MaxContextWindow)
	m.shortTermMemory.SetMaxTokens(profile.ShortTermMemoryBudget)
}

func (m *MemoryManager) AddUserMessage(content string) {
	entry := NewMemoryEntry(
		"user-"+randomSuffix(),
		content,
		MemoryTypeConversation,
		map[string]string{"source": "user"},
		EstimateTokens(content),
	)
	m.shortTermMemory.Store(entry)
	m.CompressIfNeeded()
}

func (m *MemoryManager) AddAssistantMessage(content string) {
	entry := NewMemoryEntry(
		"assistant-"+randomSuffix(),
		content,
		MemoryTypeConversation,
		map[string]string{"source": "assistant"},
		EstimateTokens(content),
	)
	m.shortTermMemory.Store(entry)
	m.CompressIfNeeded()
}

func (m *MemoryManager) AddToolResult(toolName, result string) {
	truncated := result
	runes := []rune(result)
	if len(runes) > maxToolResultChars {
		truncated = string(runes[:maxToolResultChars]) + "...(truncated)"
	}
	content := "[" + toolName + "] " + truncated
	entry := NewMemoryEntry(
		"tool-"+randomSuffix(),
		content,
		MemoryTypeToolResult,
		map[string]string{"source": "tool", "toolName": toolName},
		EstimateTokens(content),
	)
	m.shortTermMemory.Store(entry)
	m.CompressIfNeeded()
}

func (m *MemoryManager) StoreFact(fact string) {
	entry := NewMemoryEntry(
		"fact-"+randomSuffix(),
		fact,
		MemoryTypeFact,
		map[string]string{"source": "fact"},
		EstimateTokens(fact),
	)
	m.longTermMemory.Store(entry)
}

func (m *MemoryManager) RetrieveRelevant(query string, limit int) []MemoryEntry {
	return m.retriever.Retrieve(query, limit)
}

func (m *MemoryManager) BuildContextForQuery(query string, maxTokens int) string {
	return m.retriever.BuildContextForQuery(query, maxTokens)
}

func (m *MemoryManager) RecordTokenUsage(inputTokens, outputTokens int) {
	m.tokenBudget.RecordUsage(inputTokens, outputTokens)
}

func (m *MemoryManager) RecordTokenUsageWithCached(inputTokens, outputTokens, cachedInputTokens int) {
	m.tokenBudget.RecordUsageWithCached(inputTokens, outputTokens, cachedInputTokens)
}

func (m *MemoryManager) CompressIfNeeded() bool {
	if !m.tokenBudget.NeedsCompression(m.shortTermMemory, m.contextProfile.CompressionTriggerRatio) {
		return false
	}
	// 压缩前先从即将被折叠的短期记忆里抽取持久事实进长期记忆,避免随压缩丢失。
	m.compressor.ExtractFacts(m.shortTermMemory.GetAll(), m.longTermMemory)
	return m.compressor.Compress(m.shortTermMemory) != ""
}

func (m *MemoryManager) ClearShortTerm() {
	m.shortTermMemory.Clear()
}

func (m *MemoryManager) ClearLongTerm() {
	m.longTermMemory.Clear()
}

// TokenUsageCompact 返回一行紧凑的 token 用量,供 TUI header 展示。
func (m *MemoryManager) TokenUsageCompact() string {
	return m.tokenBudget.Compact()
}

func (m *MemoryManager) SystemStatus() string {
	return fmt.Sprintf(
		"context policy: %s\n%s\n%s\n%s",
		m.contextProfile.Summary(),
		m.shortTermMemory.StatusSummary(),
		m.longTermMemory.StatusSummary(),
		m.tokenBudget.UsageReport(),
	)
}

func (m *MemoryManager) ShortTermMemory() *ConversationMemory {
	return m.shortTermMemory
}

func (m *MemoryManager) LongTermMemory() *LongTermMemory {
	return m.longTermMemory
}

func (m *MemoryManager) TokenBudget() *TokenBudget {
	return m.tokenBudget
}

func (m *MemoryManager) ContextProfile() ContextProfile {
	return m.contextProfile
}
