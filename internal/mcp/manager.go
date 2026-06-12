package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
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
		// Close() 会主动关掉 stderr 管道，阻塞中的 Scan 随之返回 os.ErrClosed
		// （"file already closed"）。这是正常停机，不是错误，和 EOF 一样静默收尾。
		if err := scanner.Err(); err != nil && err != io.EOF && !errors.Is(err, os.ErrClosed) {
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
func resolveTransport(cfg MCPServerConfig) string {
	if cfg.Type != "" {
		return cfg.Type
	}
	if cfg.URL != "" {
		return "streamable-http"
	}
	return "stdio"
}

// buildStdioClient 构建基于标准输入输出(stdio)传输的MCP客户端。
// 通过fork子进程并与其建立stdin/stdout管道通信。
// 参数:
//   cfg - MCP服务器配置，包含命令、参数和环境变量
// 返回:
//   *client.Client - MCP客户端实例
//   string - 传输类型标识"stdio"
//   error - 错误信息
func buildStdioClient(cfg MCPServerConfig) (*client.Client, string, error) {
	// 验证命令不能为空
	if cfg.Command == "" {
		return nil, "", fmt.Errorf("stdio transport requires non-empty command")
	}
	// 获取当前进程环境变量，并追加配置中指定的环境变量
	env := os.Environ()
	for k, v := range cfg.Env {
		env = append(env, k+"="+expandVars(v))
	}
	// 展开命令和参数中的变量占位符（如${env:XXX}、${cwd}等）
	command := expandVars(cfg.Command)
	args := make([]string, len(cfg.Args))
	for i, a := range cfg.Args {
		args[i] = expandVars(a)
	}
	// 创建stdio客户端，启动子进程并建立管道通信
	c, err := client.NewStdioMCPClient(command, env, args...)
	if err != nil {
		return nil, "", err
	}
	return c, "stdio", nil
}

// buildStreamableHTTPClient 构建基于流式HTTP传输的MCP客户端。
// 通过HTTP/HTTPS协议与远程MCP server通信，支持流式响应。
// 参数:
//   cfg - MCP服务器配置，包含URL和自定义请求头
// 返回:
//   *client.Client - MCP客户端实例
//   string - 传输类型标识"streamable-http"
//   error - 错误信息
func buildStreamableHTTPClient(cfg MCPServerConfig) (*client.Client, string, error) {
	// 验证URL不能为空
	if cfg.URL == "" {
		return nil, "", fmt.Errorf("streamable-http transport requires non-empty url")
	}
	// 展开URL中的变量占位符
	url := expandVars(cfg.URL)
	var opts []transport.StreamableHTTPCOption
	// 如果配置了自定义请求头，将其添加到客户端选项中
	if len(cfg.Headers) > 0 {
		headers := make(map[string]string, len(cfg.Headers))
		for k, v := range cfg.Headers {
			// 同时 expand key 和 value：value 用于 Authorization: Bearer ${env:TOKEN} 这类场景。
			headers[k] = expandVars(v)
		}
		opts = append(opts, transport.WithHTTPHeaders(headers))
	}
	// 创建流式HTTP客户端
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

// Close 优雅地关闭 MCPManager，释放所有资源。
// 关闭顺序至关重要：必须先停止后台 worker，再关闭底层 client 连接。
// 如果顺序颠倒，worker 在处理通知时可能会访问已关闭的 transport channel。
// 返回:
//   error - 第一个发生的关闭错误（所有 client 都会被尝试关闭，不会因单个错误中断）
func (m *MCPManager) Close() error {
	// 1. 关闭 done channel，通知 worker 退出循环
	close(m.done)
	// 2. 等待 worker goroutine 完全退出
	m.wg.Wait()

	// 3. 逐个关闭所有 MCP client 连接
	var firstErr error
	for serverName, s := range m.servers {
		if err := s.client.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("close %s: %w", serverName, err)
		}
	}
	// 返回第一个错误（如果有的话），即使有多个错误也只报告第一个
	return firstErr
}

// extractText 把 MCP 协议中的多段 Content 合并成一段文本字符串。
// 文本内容直接拼接，非文本内容（如图像、音频等）通过 JSON 序列化兜底处理，
// 确保上层调用者不会因为遇到非文本类型的 content 而获得空串。
// 参数:
//   content - MCP协议中的内容片段列表
// 返回:
//   string - 合并后的文本字符串
func extractText(content []mcplib.Content) string {
	var b strings.Builder
	// 遍历所有内容片段
	for _, c := range content {
		switch v := c.(type) {
		// 处理值类型的 TextContent
		case mcplib.TextContent:
			b.WriteString(v.Text)
		// 处理指针类型的 TextContent
		case *mcplib.TextContent:
			b.WriteString(v.Text)
		// 非文本内容走 JSON 序列化兜底
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
	return envVarPattern.ReplaceAllStringFunc(s, func(match string) string {
		name := match[len("${env:") : len(match)-1]
		return os.Getenv(name)
	})
}

// envVarPattern 匹配 ${env:NAME}，NAME 允许字母、数字、下划线。
// 故意不允许其他字符，避免 ${env:foo}bar 这种模糊边界。
var envVarPattern = regexp.MustCompile(`\$\{env:([A-Za-z_][A-Za-z0-9_]*)\}`)