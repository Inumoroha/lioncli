package mcp

import (
	"context"
	"log"

	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// refreshKind 标识一次刷新的对象。把它从字符串 method 翻译成 enum，
// 让 worker 走干净的 switch，也方便后续加新类型。
type refreshKind int

const (
	refreshTools refreshKind = iota
	refreshResources
	refreshPrompts
)

// refreshReq 是 OnNotification 闭包丢给 worker 的最小载荷。
// 不能在闭包里直接发 RPC（参见 makeNotificationHandler 注释里关于 transport 读循环的说明）。
type refreshReq struct {
	server string
	kind   refreshKind
}

// refreshChanBuf 是 refreshCh 缓冲容量。够大让常规突发不丢，
// 满了走 handler 的 default 分支静默丢弃——丢一次也不会让状态错乱，
// 因为下次通知或下次重连会再拉。
const refreshChanBuf = 64

// makeNotificationHandler 返回挂在 *client.Client 上的通知回调。
// 注意：handler 在 transport 读循环里同步被调，**不能阻塞、不能发 RPC**
// （ListXxx 的响应也得由这个读循环送达，会死锁），所以这里只把请求扔进 channel。
func (m *MCPManager) makeNotificationHandler(serverName string) func(mcplib.JSONRPCNotification) {
	return func(n mcplib.JSONRPCNotification) {
		var kind refreshKind
		switch n.Method {
		case mcplib.MethodNotificationToolsListChanged:
			kind = refreshTools
		case mcplib.MethodNotificationResourcesListChanged:
			kind = refreshResources
		case mcplib.MethodNotificationPromptsListChanged:
			kind = refreshPrompts
		default:
			return
		}
		// 非阻塞：channel 满了直接丢。下一次通知或下一次显式刷新会兜底，
		// 在读循环里阻塞会让整个 client 失去响应，代价太大。
		select {
		case m.refreshCh <- refreshReq{server: serverName, kind: kind}:
		default:
			log.Printf("⚠️ MCP Server '%s' 刷新队列已满，丢弃 %s 通知", serverName, n.Method)
		}
	}
}

// startWorker 启动单一长生命周期 goroutine 负责处理刷新请求。
// 单 worker 既避免并发列表，也避免在 transport 读循环里发 RPC。
func (m *MCPManager) startWorker() {
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		for {
			select {
			case <-m.done:
				return
			case req := <-m.refreshCh:
				m.handleRefresh(req)
			}
		}
	}()
}

// handleRefresh 用独立 context.Background()，与触发它的请求 ctx 解绑——
// 触发者通常是 transport 的读循环或 main，没有合适的 ctx 给到这里。
// 如果将来需要全局 cancel，可以把 done 通道升级为 ctx。
func (m *MCPManager) handleRefresh(req refreshReq) {
	ctx := context.Background()
	switch req.kind {
	case refreshTools:
		m.refreshToolsFor(ctx, req.server)
	case refreshResources:
		m.refreshResourcesFor(ctx, req.server)
	case refreshPrompts:
		m.refreshPromptsFor(ctx, req.server)
	}
}

func (m *MCPManager) refreshToolsFor(ctx context.Context, serverName string) {
	s, ok := m.servers[serverName]
	if !ok {
		log.Printf("⚠️ 收到未知 server '%s' 的 tools 刷新通知，已忽略", serverName)
		return
	}
	tools, err := listAllTools(ctx, s.client)
	if err != nil {
		// 保留旧缓存：清空再失败会让 LLM 短暂看不到任何工具，比保留旧值更糟。
		log.Printf("❌ 刷新 MCP Server '%s' 的工具列表失败: %v", serverName, err)
		return
	}

	newEntries := make(map[string]toolEntry, len(tools))
	for _, t := range tools {
		lt := toLLMTool(serverName, t)
		newEntries[lt.Name] = toolEntry{
			serverName:   serverName,
			originalName: t.Name,
			llmTool:      lt,
		}
	}

	m.mu.Lock()
	for k, e := range m.tools {
		if e.serverName == serverName {
			delete(m.tools, k)
		}
	}
	for k, e := range newEntries {
		m.tools[k] = e
	}
	m.mu.Unlock()

	log.Printf("🔄 MCP Server '%s' 工具列表已刷新，当前 %d 个", serverName, len(newEntries))
	m.fireToolsChanged()
}

func (m *MCPManager) refreshResourcesFor(ctx context.Context, serverName string) {
	s, ok := m.servers[serverName]
	if !ok {
		log.Printf("⚠️ 收到未知 server '%s' 的 resources 刷新通知，已忽略", serverName)
		return
	}
	resources, err := listAllResources(ctx, s.client)
	if err != nil {
		log.Printf("❌ 刷新 MCP Server '%s' 的资源列表失败: %v", serverName, err)
		return
	}

	newEntries := make(map[string]resourceEntry, len(resources))
	for _, r := range resources {
		rr := toResource(serverName, r)
		newEntries[rr.Key] = resourceEntry{
			serverName:  serverName,
			originalURI: r.URI,
			resource:    rr,
		}
	}

	m.mu.Lock()
	for k, e := range m.resources {
		if e.serverName == serverName {
			delete(m.resources, k)
		}
	}
	for k, e := range newEntries {
		m.resources[k] = e
	}
	m.mu.Unlock()

	log.Printf("🔄 MCP Server '%s' 资源列表已刷新，当前 %d 个", serverName, len(newEntries))
	m.fireResourcesChanged()
}

func (m *MCPManager) refreshPromptsFor(ctx context.Context, serverName string) {
	s, ok := m.servers[serverName]
	if !ok {
		log.Printf("⚠️ 收到未知 server '%s' 的 prompts 刷新通知，已忽略", serverName)
		return
	}
	prompts, err := listAllPrompts(ctx, s.client)
	if err != nil {
		log.Printf("❌ 刷新 MCP Server '%s' 的 Prompt 列表失败: %v", serverName, err)
		return
	}

	newEntries := make(map[string]promptEntry, len(prompts))
	for _, p := range prompts {
		pp := toPrompt(serverName, p)
		newEntries[pp.Key] = promptEntry{
			serverName:   serverName,
			originalName: p.Name,
			prompt:       pp,
		}
	}

	m.mu.Lock()
	for k, e := range m.prompts {
		if e.serverName == serverName {
			delete(m.prompts, k)
		}
	}
	for k, e := range newEntries {
		m.prompts[k] = e
	}
	m.mu.Unlock()

	log.Printf("🔄 MCP Server '%s' Prompt 列表已刷新，当前 %d 个", serverName, len(newEntries))
	m.firePromptsChanged()
}

// OnToolsChanged 注册一个回调，工具列表通过通知刷新成功后会被调用。
// 回调在 worker goroutine 里执行（写锁已释放），可以安全地反过来调 AllLLMTools。
// Initialize 阶段的首次 load 不会触发回调——那时 main.go 自己会显式 RegisterMCP/SyncMCP。
func (m *MCPManager) OnToolsChanged(cb func()) {
	m.cbMu.Lock()
	m.toolsCBs = append(m.toolsCBs, cb)
	m.cbMu.Unlock()
}

// OnResourcesChanged 同 OnToolsChanged，但针对资源列表变更。
func (m *MCPManager) OnResourcesChanged(cb func()) {
	m.cbMu.Lock()
	m.resourcesCBs = append(m.resourcesCBs, cb)
	m.cbMu.Unlock()
}

// OnPromptsChanged 同 OnToolsChanged，但针对 Prompt 列表变更。
func (m *MCPManager) OnPromptsChanged(cb func()) {
	m.cbMu.Lock()
	m.promptsCBs = append(m.promptsCBs, cb)
	m.cbMu.Unlock()
}

// fire 系列：先复制切片，释放 cbMu 后再调，避免回调里再注册回调形成嵌套加锁。
func (m *MCPManager) fireToolsChanged()     { fireAll(m.snapshotCBs(&m.toolsCBs)) }
func (m *MCPManager) fireResourcesChanged() { fireAll(m.snapshotCBs(&m.resourcesCBs)) }
func (m *MCPManager) firePromptsChanged()   { fireAll(m.snapshotCBs(&m.promptsCBs)) }

func (m *MCPManager) snapshotCBs(slice *[]func()) []func() {
	m.cbMu.Lock()
	defer m.cbMu.Unlock()
	out := make([]func(), len(*slice))
	copy(out, *slice)
	return out
}

func fireAll(cbs []func()) {
	for _, cb := range cbs {
		cb()
	}
}
