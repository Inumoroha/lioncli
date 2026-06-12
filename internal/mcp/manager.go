package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// serverState 保存一个已连接 server 的 client 和它在 Initialize 时声明的 capabilities。
// 我们用 capabilities 来决定要不要去调对应的 list 接口，避免对不支持的 server 发无意义请求。
type serverState struct {
	client       *client.Client
	capabilities mcplib.ServerCapabilities
}

// MCPManager 管理所有连接到的 MCP server，并把它们提供的 tools/resources/prompts
// 聚合成统一视图。具体的领域操作分散在同包的 tool.go / resource.go / prompt.go，
// refresh / 通知通路在 refresh.go——这里只放骨架（生命周期 + 共享状态）。
type MCPManager struct {
	servers   map[string]*serverState
	tools     map[string]toolEntry     // key 为 keyOf(serverName, originalName)
	resources map[string]resourceEntry // key 为 keyOf(serverName, originalURI)
	prompts   map[string]promptEntry   // key 为 keyOf(serverName, originalName)

	// mu 保护上面三张 map。读路径走 RLock，refresh 写路径走 Lock。
	// servers 不在 mu 范围内：握手阶段独占式构建，之后只读。
	mu sync.RWMutex

	// refreshCh 由 OnNotification handler 写入、worker 读取，
	// 把 transport 读循环的同步回调与实际 RPC 调用解耦。
	refreshCh chan refreshReq
	done      chan struct{}
	wg        sync.WaitGroup

	// cbMu 保护下面三组回调切片。回调由 worker 在写锁释放后调用，
	// 这样回调里反过来调 mgr.AllLLMTools() 不会与 refresh 自身死锁。
	cbMu         sync.Mutex
	toolsCBs     []func()
	resourcesCBs []func()
	promptsCBs   []func()
}

func NewMCPManager(mcpConfig *MCPConfig) *MCPManager {
	m := &MCPManager{
		servers:   make(map[string]*serverState),
		tools:     make(map[string]toolEntry),
		resources: make(map[string]resourceEntry),
		prompts:   make(map[string]promptEntry),
		refreshCh: make(chan refreshReq, refreshChanBuf),
		done:      make(chan struct{}),
	}

	for serverName, serverConfig := range mcpConfig.MCPServers {
		mcpClient, transportName, err := buildClient(serverConfig)
		if err != nil {
			log.Printf("❌ 启动 MCP Server '%s' 失败: %v", serverName, err)
			continue
		}
		if transportName == "stdio" {
			// stderr 在 buildClient 拿不到 serverName，统一在这里挂泵。
			pumpStderr(mcpClient, serverName)
		}
		m.servers[serverName] = &serverState{client: mcpClient}
		log.Printf("✅ 启动 MCP Server '%s' (%s) 成功！", serverName, transportName)
	}
	return m
}

// pumpStderr 把 stdio 子进程的 stderr 按行转 log。
// 不接的话数据会在 OS 管道里堆，缓冲满了子进程 write 会阻塞，
// 而且我们也看不到 npx/uvx 启动失败、依赖缺失之类的线索。
// goroutine 在 stderr EOF（子进程退出）时自动结束，不需要显式 stop。
func pumpStderr(c *client.Client, serverName string) {
	r, ok := client.GetStderr(c)
	if !ok || r == nil {
		return
	}
	go func() {
		scanner := bufio.NewScanner(r)
		// 单行 1MB 上限：默认 64KB 容易被 traceback / 大 JSON 塞爆，
		// 报错被截断比噪音更可怕。
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			log.Printf("📥 [mcp:%s] %s", serverName, scanner.Text())
		}
		if err := scanner.Err(); err != nil && err != io.EOF {
			log.Printf("⚠️ [mcp:%s] stderr 读取错误: %v", serverName, err)
		}
	}()
}

// buildClient 按 server 配置生成对应 transport 的客户端。
// transport 推断规则：显式 Type 优先；未填则有 URL 走 http，否则 stdio。
// 返回的字符串供日志使用，方便排查接的是哪条通路。
func buildClient(cfg MCPServerConfig) (*client.Client, string, error) {
	switch resolveTransport(cfg) {
	case "stdio":
		return buildStdioClient(cfg)
	case "http", "streamable-http":
		return buildStreamableHTTPClient(cfg)
	default:
		// 走到这里说明配了不认识的 type，明确报错好过沉默回退。
		return nil, "", fmt.Errorf("unsupported transport type: %q", cfg.Type)
	}
}


// resolveTransport 根据 server 配置推断 transport 类型。
// 显式 Type 优先；未填则有 URL 走 http，否则 stdio。
func resolveTransport(cfg MCPServerConfig) string {
	if cfg.Type != "" {
		return cfg.Type
	}
	if cfg.URL != "" {
		return "streamable-http"
	}
	return "stdio"
}

func buildStdioClient(cfg MCPServerConfig) (*client.Client, string, error) {
	if cfg.Command == "" {
		return nil, "", fmt.Errorf("stdio transport requires non-empty command")
	}
	env := os.Environ()
	for k, v := range cfg.Env {
		env = append(env, k+"="+expandVars(v))
	}
	command := expandVars(cfg.Command)
	args := make([]string, len(cfg.Args))
	for i, a := range cfg.Args {
		args[i] = expandVars(a)
	}
	c, err := client.NewStdioMCPClient(command, env, args...)
	if err != nil {
		return nil, "", err
	}
	return c, "stdio", nil
}

func buildStreamableHTTPClient(cfg MCPServerConfig) (*client.Client, string, error) {
	if cfg.URL == "" {
		return nil, "", fmt.Errorf("streamable-http transport requires non-empty url")
	}
	url := expandVars(cfg.URL)
	var opts []transport.StreamableHTTPCOption
	if len(cfg.Headers) > 0 {
		headers := make(map[string]string, len(cfg.Headers))
		for k, v := range cfg.Headers {
			// 同时 expand key 和 value：value 用于 Authorization: Bearer ${env:TOKEN} 这类场景。
			headers[k] = expandVars(v)
		}
		opts = append(opts, transport.WithHTTPHeaders(headers))
	}
	c, err := client.NewStreamableHttpClient(url, opts...)
	if err != nil {
		return nil, "", err
	}
	return c, "streamable-http", nil
}

// initializeTimeout 是单个 server 握手的硬上限。
// 一个慢/卡死的 server 不能拖累其他 server 的可用性——这个超时只 cancel 它自己的 Initialize。
// 10s 对正常 server 绰绰有余，对挂死的 server 也不会让用户等太久。
const initializeTimeout = 10 * time.Second

// maxListPages 是分页 list 调用的最大页数兜底。
// MCP 协议没有上限，但实现有 bug 的 server 可能给出固定的 NextCursor 造成死循环。
// 500 页 × 常见的 50~100 条/页 = 数万条，对单个 server 已经远超合理规模。
const maxListPages = 500

// Initialize 与所有 server 并发完成握手，记录其 capabilities，挂上通知监听并启动
// 后台 worker，然后按声明的 capability 预加载 tools / resources / prompts。
// 任一 server 失败（包括超时）只跳过该 server，不影响其他。
func (m *MCPManager) Initialize(ctx context.Context) {
	initReq := mcplib.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcplib.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcplib.Implementation{
		Name:    "tea-cli",
		Version: "1.0.0",
	}

	// 并发握手：串行的话 N 个 server 的最坏延迟是 N * timeout。
	// 写 m.servers / capabilities 不需要锁——这阶段没有其他 goroutine 读它。
	type hsResult struct {
		name string
		caps mcplib.ServerCapabilities
		err  error
	}
	results := make(chan hsResult, len(m.servers))
	var wg sync.WaitGroup
	for serverName, s := range m.servers {
		wg.Add(1)
		go func(name string, st *serverState) {
			defer wg.Done()
			hctx, cancel := context.WithTimeout(ctx, initializeTimeout)
			defer cancel()
			res, err := st.client.Initialize(hctx, initReq)
			if err != nil {
				results <- hsResult{name: name, err: err}
				return
			}
			results <- hsResult{name: name, caps: res.Capabilities}
		}(serverName, s)
	}
	wg.Wait()
	close(results)

	for r := range results {
		s := m.servers[r.name]
		if r.err != nil {
			log.Printf("❌ 初始化握手 MCP Server '%s' 失败: %v", r.name, r.err)
			// 关掉底层连接，避免子进程 / HTTP session 泄漏。
			_ = s.client.Close()
			delete(m.servers, r.name)
			continue
		}
		s.capabilities = r.caps
		// 通知监听：handler 在 transport 读循环里被同步调，绝对不能阻塞或发 RPC，
		// 所以 makeNotificationHandler 只把请求塞进 channel 给 worker。
		s.client.OnNotification(m.makeNotificationHandler(r.name))
		log.Printf("🚀 ✅ MCP Server '%s' 管道连接成功，握手已完成！", r.name)
	}

	// 在 loadXxx 之前启 worker：即便 server 在 load 期间就推通知，
	// worker 也已经在听 refreshCh，不会丢。
	m.startWorker()

	m.loadTools(ctx)
	m.loadResources(ctx)
	m.loadPrompts(ctx)
}

// Close 先通知 worker 退出并等其结束，再关闭所有底层 client。
// 顺序很重要：worker 还在用 client 时不能关，否则会撞到 transport 的 closed channel。
func (m *MCPManager) Close() error {
	close(m.done)
	m.wg.Wait()

	var firstErr error
	for serverName, s := range m.servers {
		if err := s.client.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("close %s: %w", serverName, err)
		}
	}
	return firstErr
}

// extractText 把 mcp 协议里多段 Content 合成一段文本。非文本段走 JSON
// 兜底序列化，确保上层不会因为遇到 image/audio 等 content 而拿到空串。
func extractText(content []mcplib.Content) string {
	var b strings.Builder
	for _, c := range content {
		switch v := c.(type) {
		case mcplib.TextContent:
			b.WriteString(v.Text)
		case *mcplib.TextContent:
			b.WriteString(v.Text)
		default:
			if raw, err := json.Marshal(c); err == nil {
				b.Write(raw)
			}
		}
	}
	return b.String()
}

// expandVars 替换 mcp.json 中配置的占位符为实际值。
// ${cwd} / ${home} 取当前工作目录与用户主目录。
// ${comspec} 仅 Windows 有意义（典型值 C:\Windows\System32\cmd.exe），
// 用绝对路径绕开 Go 1.20+ 对 cwd 同名可执行文件的拒绝执行策略；
// 非 Windows 上 ComSpec 通常为空字符串，需要的话由 mcp.json 改写。
// ${env:NAME} 读任意环境变量，主要给 streamable-http 的 headers 注入 token 用，
// 避免把 secret 明文写在 mcp.json 里。
func expandVars(s string) string {
	cwd, _ := os.Getwd()
	home, _ := os.UserHomeDir()

	replacements := map[string]string{
		"${cwd}":     cwd,
		"${home}":    home,
		"${comspec}": os.Getenv("ComSpec"),
	}
	for placeholder, value := range replacements {
		s = strings.ReplaceAll(s, placeholder, value)
	}

	// 替换 ${env:NAME} 为实际值,ReplaceAllStringFunc 的作用是将匹配到的字符串替换为一个函数返回的字符串。
	// 这里我们使用一个匿名函数，该函数的参数是匹配到的字符串，返回值是环境变量的值
	// 这里我们使用 os.Getenv 函数获取环境变量的值，如果环境变量不存在，则返回空字符串。
	return envVarPattern.ReplaceAllStringFunc(s, func(match string) string {
		name := match[len("${env:") : len(match)-1]
		return os.Getenv(name)
	})
}

// envVarPattern 匹配 ${env:NAME}，NAME 允许字母、数字、下划线。
// 故意不允许其他字符，避免 ${env:foo}bar 这种模糊边界。
var envVarPattern = regexp.MustCompile(`\$\{env:([A-Za-z_][A-Za-z0-9_]*)\}`)
