# 1. 骨架:让"连接→列工具"一条线先跑通

## mcp.json

```json
{
    "mcpServers": {
        "filesystem": {
            "type": "stdio",
            "command": "${comspec}",
            "args": [
                "/c",
                "npx",
                "-y",
                "@modelcontextprotocol/server-filesystem",
                "${cwd}"
            ]
        },
        "server-everything-http": {
            "type": "streamable-http",
            "url": "http://localhost:3001/mcp"
        },
        "chrome-devtools": {
            "command": "${comspec}",
            "args": [
                "/c",
                "npx",
                "-y",
                "chrome-devtools-mcp@latest",
                "--autoConnect"
            ]
        }
    }
}
```

## config.go

```go
// MCPServerConfig 描述单个 MCP server 的接入方式。两种 transport 共用同一结构，
// 通过 Type 显式声明；未填则按 URL 是否存在自动推断（有 URL = http，否则 stdio）。
type MCPServerConfig struct {
	// Type 取值："stdio" | "http" | "streamable-http"。后两者等价，都走 Streamable HTTP。
	// 留空时按 URL 是否存在推断，便于已有 stdio 配置无感升级。
	Type string `json:"type,omitempty"`

	// stdio 专用
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`

	// streamable-http 专用
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

type MCPConfig struct {
	MCPServers map[string]MCPServerConfig `json:"mcpServers"`
}
```

- **核心**:就两个 struct(`MCPServerConfig` / `MCPConfig`),理解"一条配置 = 一个 server 怎么连"。
- **看点**:`Type` 留空时如何按 URL 推断 transport(stdio vs http)。
- **可跳过**:json tag 的细节,扫一眼即可。
- ✅ 自测:一个 stdio server 和一个 http server 的配置分别长什么样?

## manager.go(上半 · 建连接部分) 

### 模型

```go
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
```

**核心难点**:`MCPManager` 那几个字段为什么这么分组 —— 三张 map + 一把 `mu` 锁 + channel/worker 那组 + 回调那组。先有个印象,后面会逐个用到。

✅ 自测:`servers` 这张 map 为什么注释说"不在 mu 保护范围内"?

### 工厂函数

```go
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
```

#### buildClient

```go
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
```

##### resolveTransport

```go
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
```

##### buildStdioClient

```go
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
```

##### buildStreamableHTTPClient

```go
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
```

`expandVars`/`envVarPattern`(变量替换,纯字符串处理,与主线无关)。

##### expandVars

```go
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
```

#### pumpStderr

```go
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
```

`pumpStderr`(stderr 转日志,边角)

## tool.go (下半 · load 部分)

### 前缀防撞名方案

```go
// toolPrefixSep 用于把 serverName 和原始名字拼接，避免不同 server 间冲突。
// tools / resources / prompts 共用一套前缀方案：mcp__<server>__<original>。
const toolPrefixSep = "__"

// KeyPrefix 是所有 MCP 工具/资源/prompt key 共有的前缀（"mcp__"）。
// 导出供 internal/tool 等下游按前缀做整批操作（比如 SyncMCP 时清掉旧条目）。
const KeyPrefix = "mcp" + toolPrefixSep

// keyOf 生成对外暴露的、可路由回 (server, original) 的键。
func keyOf(serverName, original string) string {
	return KeyPrefix + serverName + toolPrefixSep + original
}
```

`mcp__<server>__<original>` 前缀:为什么需要(多 server 工具名会撞)。

### 模型

```go
// toolEntry 记录一个工具属于哪个 server，以及它在 server 内的原始名字。
// LLM 看到的工具名是带前缀的，调用时需要按 entry 路由回去。
type toolEntry struct {
	serverName   string
	originalName string
	llmTool      llm.Tool
}
```

### toLLMTool

```go
// toLLMTool 将 MCP 协议定义的工具转换为 LLM 工具格式。
// 通过在工具名称前添加服务器名称前缀，确保不同服务器的同名工具不会冲突。
// 参数:
//   serverName - MCP服务器名称
//   t - MCP协议定义的工具对象
// 返回:
//   llm.Tool - LLM可识别的工具格式
func toLLMTool(serverName string, t mcplib.Tool) llm.Tool {
	return llm.Tool{
		Name:        keyOf(serverName, t.Name), // 使用keyOf生成带服务器前缀的唯一名称
		Description: t.Description,             // 直接使用原工具描述
		Parameters:  toMapSchema(t.InputSchema), // 将输入模式转换为LLM参数格式
	}
}
```

#### toMapSchema

```go
// toMapSchema 将 MCP 工具输入模式转换为 LLM 可识别的 JSON Schema 格式。
// 只包含非空字段，避免传递不必要的空值给 LLM。
// 参数:
//   s - MCP协议定义的工具输入模式
// 返回:
//   map[string]interface{} - JSON Schema 格式的参数定义
func toMapSchema(s mcplib.ToolInputSchema) map[string]interface{} {
	m := map[string]interface{}{
		"type": s.Type, // JSON Schema 类型
	}
	if s.Properties != nil {
		m["properties"] = s.Properties // 属性定义
	}
	if len(s.Required) > 0 {
		m["required"] = s.Required // 必填字段列表
	}
	if s.Defs != nil {
		m["$defs"] = s.Defs // 可复用的定义引用
	}
	if s.AdditionalProperties != nil {
		m["additionalProperties"] = s.AdditionalProperties // 额外属性约束
	}
	return m
}
```

`toMapSchema` 里逐个字段的拷贝,知道"把 mcp 的 schema 转成 llm 的"即可。

### loadTools

```go
// loadTools 拉取每个声明了 tools capability 的 server 的工具清单（分页），
// 写入 m.tools。Initialize 阶段一次性调用，每页写入持 m.mu 写锁。
func (m *MCPManager) loadTools(ctx context.Context) {
	log.Println("=== 🧭 正在获取所有 MCP Server 提供的工具列表 ===")
	for serverName, s := range m.servers {
		if s.capabilities.Tools == nil {
			continue
		}
		tools, err := listAllTools(ctx, s.client)
		if err != nil {
			log.Printf("❌ 获取 MCP Server '%s' 的工具列表失败: %v", serverName, err)
			continue
		}
		m.mu.Lock()
		for _, t := range tools {
			lt := toLLMTool(serverName, t)
			m.tools[lt.Name] = toolEntry{
				serverName:   serverName,
				originalName: t.Name,
				llmTool:      lt,
			}
			log.Printf("✅ MCP Server '%s' 提供工具: %s", serverName, lt.Name)
		}
		m.mu.Unlock()
	}
}
```

### listAllTools

```go
// listAllTools 走完所有分页，返回 server 上的全部工具。
// 上限 maxListPages 是防御性熔断：行为有问题的 server 可能给出固定的 NextCursor 让客户端死循环。
// 参数 ctx: 上下文，用于控制调用超时和取消
// 参数 c: MCP 客户端，用于与 server 通信
// 返回值: 所有工具列表，以及可能的错误
func listAllTools(ctx context.Context, c *client.Client) ([]mcplib.Tool, error) {
	// 初始化结果切片和分页游标
	var out []mcplib.Tool
	var cursor mcplib.Cursor

	// 循环获取所有分页，最多遍历 maxListPages 页（防御性熔断）
	for page := 0; page < maxListPages; page++ {
		// 构造分页请求
		req := mcplib.ListToolsRequest{}
		req.Params.Cursor = cursor

		// 调用 server 的 ListTools 接口
		res, err := c.ListTools(ctx, req)
		if err != nil {
			return nil, err
		}

		// 将当前页的工具追加到结果中
		out = append(out, res.Tools...)

		// 如果没有下一页游标，说明已经获取完所有工具
		if res.NextCursor == "" {
			return out, nil
		}

		// 更新游标，准备获取下一页
		cursor = res.NextCursor
	}

	// 超出分页上限，返回错误（疑似 server 游标行为异常）
	return nil, fmt.Errorf("分页超出上限 %d，疑似 server 游标行为异常", maxListPages)
}
```

`listAllTools` 的**分页循环** + `maxListPages` 熔断(防 server 给死循环游标)。

✅ 自测:为什么要 `maxListPages` 这个上限?去掉会怎样?

🏁 **到这里,作者已有一个能"启动→连接→列工具"一次性跑通的程序了。这就是骨架。**

# 2. 做扎实:多 server 并发 + 生命周期 + 真正调用

## manager.go (下半 · Initialize / Close)

### Initialize

```go
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
```

**并发握手** `Initialize`:为什么用 goroutine 并发(串行最坏 = N×timeout)。

**循环变量捕获坑**:`go func(name, st)` 为什么把循环变量当参数传 → 闭包按引用捕获 + goroutine 异步 = 都读到最后一个值。(Go 1.22 已修复语义,但要会讲)

每个 server 独立 `context.WithTimeout`:一个卡死不拖累别人。

### Close

```go
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
```

**`Close` 的顺序**:为什么必须先 `close(done)`+`wg.Wait()` 关 worker,**再**关 client(worker 还在用 client 时关会撞 closed channel)。

**可跳过**:`extractText`(多段 content 拼文本,工具函数)。

✅ 自测:不传 `name` 参数、直接用循环变量,3 个 server 会发生什么?

### extractText

```go
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
```

## tool.go (剩余 · CallTool / AllLLMTools) 

### AllLLMTools

```go
// AllLLMTools 返回 LLM 可调用的全部工具描述。
// 该方法是线程安全的，通过读锁保护共享数据
// 返回值: 所有已加载工具的 llm.Tool 描述列表
func (m *MCPManager) AllLLMTools() []llm.Tool {
	// 获取读锁，确保并发安全
	m.mu.RLock()
	defer m.mu.RUnlock()

	// 预分配结果切片容量，优化性能
	out := make([]llm.Tool, 0, len(m.tools))

	// 遍历所有工具条目，收集 LLM 可用的工具描述
	for _, e := range m.tools {
		out = append(out, e.llmTool)
	}

	return out
}
```

### CallTool

```go
// CallTool 把 LLM 给出的带前缀名字路由到对应 server 调用。
// 工具自身返回 IsError=true 时，把它给出的文本一并塞进 error，避免 agent 丢失原因。
// 参数 ctx: 上下文，用于控制调用超时和取消
// 参数 name: LLM 看到的带前缀的工具名（格式: mcp__<server>__<original>）
// 参数 args: 工具调用的参数映射
// 返回值: 工具执行结果文本，以及可能的错误
func (m *MCPManager) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	// 读取工具映射，查找对应的工具条目
	m.mu.RLock()
	entry, ok := m.tools[name]
	m.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", name)
	}

	// 查找工具所属的服务器连接
	s, ok := m.servers[entry.serverName]
	if !ok {
		return "", fmt.Errorf("server %s not connected", entry.serverName)
	}

	// 构造工具调用请求，使用原始工具名（不带前缀）
	req := mcplib.CallToolRequest{}
	req.Params.Name = entry.originalName
	req.Params.Arguments = args

	// 调用远程服务器的工具
	res, err := s.client.CallTool(ctx, req)
	if err != nil {
		return "", err
	}

	// 提取返回内容中的文本
	text := extractText(res.Content)

	// 如果工具返回错误，将结果文本包装进 error 返回
	if res.IsError {
		return text, fmt.Errorf("tool %s failed: %s", name, text)
	}

	return text, nil
}
```

- **核心**:`CallTool` 如何把带前缀的名字**路由回**原 server(查 entry → 拿 originalName → 找 client → 调用)。
- **看点**:`IsError=true` 时把文本塞进 error,避免 agent 丢失失败原因。
- ✅ 自测:LLM 给的名字是 `mcp__github__search`,CallTool 怎么找到该调哪个 server 的哪个工具?

# 3. 最难:刷新机制(作者一定踩过死锁才写出来)

##  refresh.go

### 模型

```go
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
```

### makeNotificationHandler

```go
// makeNotificationHandler 返回挂在 *client.Client 上的通知回调。
// 注意：handler 在 transport 读循环里同步被调，不能阻塞、不能发 RPC
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
```

### startWorker

```go
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
```

### handleRefresh

```go
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
```

#### refreshToolsFor

```go
// refreshToolsFor 刷新指定 MCP server 的工具列表。
// 当收到 server 发送的工具变更通知时调用此方法，重新拉取工具清单并更新缓存。
// 刷新失败时保留旧缓存，避免 LLM 短暂无法使用工具。
// 参数:
//   ctx - 上下文，用于控制请求超时
//   serverName - 要刷新的 MCP server 名称
func (m *MCPManager) refreshToolsFor(ctx context.Context, serverName string) {
	// 查找对应的 server 状态
	s, ok := m.servers[serverName]
	if !ok {
		log.Printf("⚠️ 收到未知 server '%s' 的 tools 刷新通知，已忽略", serverName)
		return
	}
	// 从 server 拉取最新工具列表
	tools, err := listAllTools(ctx, s.client)
	if err != nil {
		// 保留旧缓存：清空再失败会让 LLM 短暂看不到任何工具，比保留旧值更糟。
		log.Printf("❌ 刷新 MCP Server '%s' 的工具列表失败: %v", serverName, err)
		return
	}

	// 将工具转换为 LLM 格式并构建新的条目映射
	newEntries := make(map[string]toolEntry, len(tools))
	for _, t := range tools {
		lt := toLLMTool(serverName, t)
		newEntries[lt.Name] = toolEntry{
			serverName:   serverName,
			originalName: t.Name,
			llmTool:      lt,
		}
	}

	// 更新工具缓存（加写锁）
	m.mu.Lock()
	// 先删除该 server 的所有旧工具
	for k, e := range m.tools {
		if e.serverName == serverName {
			delete(m.tools, k)
		}
	}
	// 再添加新工具
	for k, e := range newEntries {
		m.tools[k] = e
	}
	m.mu.Unlock()

	// 记录刷新日志并触发回调通知
	log.Printf("🔄 MCP Server '%s' 工具列表已刷新，当前 %d 个", serverName, len(newEntries))
	m.fireToolsChanged()
}
```

### OnToolsChanged

```go
// OnToolsChanged 注册一个回调，工具列表通过通知刷新成功后会被调用。
// 回调在 worker goroutine 里执行（写锁已释放），可以安全地反过来调 AllLLMTools。
// Initialize 阶段的首次 load 不会触发回调——那时 main.go 自己会显式 RegisterMCP/SyncMCP。
func (m *MCPManager) OnToolsChanged(cb func()) {
	m.cbMu.Lock()
	m.toolsCBs = append(m.toolsCBs, cb)
	m.cbMu.Unlock()
}
```

```go
// fire 系列：先复制切片，释放 cbMu 后再调，避免回调里再注册回调形成嵌套加锁。
func (m *MCPManager) fireToolsChanged() {
    fireAll(m.snapshotCBs(&m.toolsCBs)) 
}

// fireAll 依次执行传入的所有回调函数
// cbs: 待执行的回调函数切片
func fireAll(cbs []func()) {
	for _, cb := range cbs {
		cb()
	}
}
```

### snapshotCBs

```go
// snapshotCBs 创建回调函数切片的快照副本
// 在持有 cbMu 锁的情况下复制切片，确保并发安全
// slice: 指向回调函数切片的指针
// 返回值: 回调函数切片的副本
func (m *MCPManager) snapshotCBs(slice *[]func()) []func() {
	m.cbMu.Lock()
	defer m.cbMu.Unlock()
	out := make([]func(), len(*slice))
	copy(out, *slice)
	return out
}
```

**必啃核心(这是整个模块的灵魂)**:

- **single-reader 自死锁**:transport 单读循环复用"响应+通知";handler 是同步回调;在 handler 里发 RPC → 响应要同一读循环接收,而它正卡在 handler → 自死锁。
- **解法 = channel + worker 解耦**:`makeNotificationHandler` 只翻译+非阻塞投递;`startWorker` 后台死循环取出来才真正发 RPC。
- **`select{case ch<-x: default:}` 非阻塞投递**:满了就丢(下次通知/重连兜底),绝不在读循环里阻塞。
- `refreshToolsFor` 的**写锁删改 map**:先删该 server 旧 entry,再塞新的 → 对应 status.go 的 RLock 读,抢同一把 `mu`。
- **fire 系列的回调重入死锁**:`snapshotCBs` 先复制切片再出锁调回调,避免回调里又来注册回调形成嵌套加锁。(与 single-reader 是"同类病不同表现")

✅ 自测:为什么 handler 不能直接调 ListTools?用自己的话讲一遍死锁。

# 4. 复制扩展 + 收尾(最省力,可快速扫过)

##  resource.go + prompt.go克隆,快速扫

- **本质**:tool.go 那套(entry / toXxx / loadXxx / listAllXxx / AllXxx / 路由调用)**整体复制改名**。
- **只需对比差异**:
  - resource 的 key 用 **URI**(不是 name);返回 `ResourceContent` 可能含 **Blob(二进制 base64)**。
  - `toResourceContents` 的 type switch:同时匹配值/指针两种形态 + JSON 兜底(协议反序列化形态不固定)。
- **可跳过**:逐行读 —— 看懂 tool.go 后这里没有新概念,**确认"果然一样"即可**。
- ✅ 自测:resource 的 key 为什么用 URI 而不是 name?

## status.go

### 模型

```go
type ServerStatus struct {
	Name      string
	Tools     int
	Resources int
	Prompts   int
}

type StatusSnapshot struct {
	Servers []ServerStatus
}
```

### StatusSnapshot

```go
// StatusSnapshot 获取 MCPManager 当前状态的快照
// 该方法是线程安全的，通过读锁保护共享数据
// 返回值: 包含所有已连接服务器状态的快照，按服务器名称排序
func (m *MCPManager) StatusSnapshot() StatusSnapshot {
	// 处理 nil 接收器，返回空快照
	if m == nil {
		return StatusSnapshot{}
	}
	// 获取读锁，确保并发安全
	m.mu.RLock()
	defer m.mu.RUnlock()

	// 初始化服务器状态映射，预分配容量以优化性能
	byName := make(map[string]*ServerStatus, len(m.servers))
	for name := range m.servers {
		byName[name] = &ServerStatus{Name: name}
	}

	// 统计每个服务器的工具数量
	for _, entry := range m.tools {
		status := ensureServerStatus(byName, entry.serverName)
		status.Tools++
	}

	// 统计每个服务器的资源数量
	for _, entry := range m.resources {
		status := ensureServerStatus(byName, entry.serverName)
		status.Resources++
	}

	// 统计每个服务器的提示词数量
	for _, entry := range m.prompts {
		status := ensureServerStatus(byName, entry.serverName)
		status.Prompts++
	}

	// 提取服务器名称并按字母排序
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)

	// 构建有序的结果切片
	out := StatusSnapshot{Servers: make([]ServerStatus, 0, len(names))}
	for _, name := range names {
		out.Servers = append(out.Servers, *byName[name])
	}

	return out
}
```

### FormatStatus

```go
// FormatStatus 将 MCP 服务器状态快照格式化为人类可读的字符串
// 参数 snapshot: 包含所有已连接服务器状态的快照
// 返回值: 格式化后的状态信息字符串
func FormatStatus(snapshot StatusSnapshot) string {
	// 如果没有已连接的服务器，返回提示信息
	if len(snapshot.Servers) == 0 {
		return "MCP: no connected servers."
	}
	// 创建结果行列表，首行显示服务器总数
	lines := []string{fmt.Sprintf("MCP servers: %d", len(snapshot.Servers))}
	// 遍历每个服务器，格式化输出其详细信息
	for _, server := range snapshot.Servers {
		lines = append(lines, fmt.Sprintf(
			"  %s: %d tools, %d resources, %d prompts",
			server.Name,
			server.Tools,
			server.Resources,
			server.Prompts,
		))
	}
	// 将所有行用换行符连接并返回
	return strings.Join(lines, "\n")
}
```

### ensureServerStatus

```go
// ensureServerStatus 确保指定名称的 ServerStatus 存在于 map 中
// 采用 get-or-create 模式：如果已存在则返回现有实例，否则创建新实例并添加到 map
// 参数 status: 存储 ServerStatus 的 map
// 参数 name: 服务器名称
// 返回值: 对应的 ServerStatus 指针（可能是已有或新建的）
func ensureServerStatus(status map[string]*ServerStatus, name string) *ServerStatus {
	// 检查是否已存在该服务器的状态记录
	if existing := status[name]; existing != nil {
		return existing
	}
	// 创建新的 ServerStatus 实例
	created := &ServerStatus{Name: name}
	// 将新实例添加到 map 中
	status[name] = created
	return created
}
```

**核心难点:

- nil 接收器检查 → 方法分发不解引用,进方法体才 panic。
- `map[string]*ServerStatus` 用**指针** → map 元素不可寻址,值类型 `m[k].Tools++` 编译不过。
- 取不存在 key 返回**零值**(nil 指针)→ `ensureServerStatus` 防 nil 解引用 panic。
- **排序**:map 遍历故意随机 → 不排序输出顺序不定 → 测试 flaky。排序 = 确定性输出。

**可跳过**:`FormatStatus`(纯字符串拼接,无难点)。

✅ 自测:既然 servers 都预填进 byName 了,为什么统计时还要 `ensureServerStatus` 而不直接 `byName[name].Tools++`?

# 5. 一句话路线图

config → manager(连接) → tool(load/call) → **refresh(死磕)** → resource/prompt(略过) → status(复习)

> 难点其实就 3 块:**循环变量捕获**(阶段1)、**single-reader 死锁 + 非阻塞 channel**(阶段2)、**map 不可寻址/nil/flaky**(阶段3)。
> 这 3 块吃透,整个模块就是你的了。其余全是样板和克隆。