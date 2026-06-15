package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"lioncli/internal/agent"
	"lioncli/internal/llm"
	"lioncli/internal/llm/openai"
	"lioncli/internal/mcp"
	"lioncli/internal/memory"
	"lioncli/internal/plan"
	"lioncli/internal/prompt"
	"lioncli/internal/rag"
	"lioncli/internal/skill"
	"lioncli/internal/tool"
	"lioncli/internal/tool/builtin" // 既触发内置工具 init() 自动注册，也用于构造 web 工具
	"lioncli/internal/tui"
	"lioncli/internal/web"
)

func main() {
	// .env 与代码同库不入仓,这里手动加载,避免引第三方依赖。
	if err := loadDotenv(); err != nil {
		fmt.Fprintf(os.Stderr, "加载 .env 失败: %v\n", err)
		os.Exit(1)
	}

	apiKey := os.Getenv("AI_API_KEY")
	baseURL := os.Getenv("AI_BASE_URL")
	model := os.Getenv("AI_MODEL")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "缺少 AI_API_KEY,请检查 .env")
		os.Exit(1)
	}

	// .env 里是 DeepSeek,走 OpenAI 兼容协议,故用 openai provider。
	opts := []openai.Option{}
	if baseURL != "" {
		opts = append(opts, openai.WithBaseURL(baseURL))
	}
	if model != "" {
		opts = append(opts, openai.WithDefaultModel(model))
	}
	client := openai.New(apiKey, opts...)

	fmt.Printf("provider=openai-compatible  base=%s  model=%s\n", baseURL, model)

	// MCP server 握手 + 拉工具清单可能较慢(子进程冷启动 / 远程握手),
	// 给它一个独立超时,别和后面的 chat 共用一个时钟。
	mcpCtx, mcpCancel := context.WithTimeout(context.Background(), 40*time.Second)
	manager := startMCP(mcpCtx)
	mcpCancel()
	if manager != nil {
		defer manager.Close()
	}

	registry := buildRegistry(manager)
	appAgent := buildAgent(client, registry, model)
	if err := tui.Run(appAgent, model, mcpStatusFunc(manager)); err != nil {
		fmt.Fprintf(os.Stderr, "TUI 运行失败: %v\n", err)
		os.Exit(1)
	}
}

func buildRegistry(manager *mcp.MCPManager) *tool.ToolRegistry {
	registry := tool.NewToolRegistry()
	registerWebTools(registry)
	if manager != nil {
		if err := registry.RegisterMCP(manager); err != nil {
			fmt.Fprintf(os.Stderr, "注册 MCP 工具失败: %v\n", err)
		}
		if err := tool.RegisterMCPHelpers(registry, manager); err != nil {
			fmt.Fprintf(os.Stderr, "注册 MCP 资源工具失败: %v\n", err)
		}
		manager.OnToolsChanged(func() {
			if err := registry.SyncMCP(manager); err != nil {
				fmt.Fprintf(os.Stderr, "刷新 MCP 工具失败: %v\n", err)
			}
		})
	}
	return registry
}

func buildAgent(client llm.Client, registry *tool.ToolRegistry, model string) *agent.Agent {
	skillReg := skill.NewSkillRegistry(skill.BuiltinFS(), skill.BuiltinRoot(), "", "", nil)
	skillReg.Reload()
	skillBuf := skill.NewSkillContextBuffer()
	if err := registry.RegisterTool(builtin.NewLoadSkillTool(skillReg, skillBuf)); err != nil {
		fmt.Fprintf(os.Stderr, "注册 load_skill 失败: %v\n", err)
	}

	memoryAdapter := memory.NewLLMChatAdapter(client, model)
	memMgr, err := memory.NewMemoryManager(memoryAdapter)
	if err != nil {
		fmt.Fprintf(os.Stderr, "初始化记忆系统失败: %v\n", err)
	}

	planner := plan.NewPlanner(plan.NewLLMChatAdapter(client, model), nil)
	embedder := rag.NewEmbeddingClientFromEnv()
	return agent.New(client, registry, model, skillReg, skillBuf, prompt.NewDefaultAssembler(), memMgr, planner, embedder)
}

func mcpStatusFunc(manager *mcp.MCPManager) func() mcp.StatusSnapshot {
	return func() mcp.StatusSnapshot {
		if manager == nil {
			return mcp.StatusSnapshot{}
		}
		return manager.StatusSnapshot()
	}
}

// startMCP 读取 mcp.json,启动所有 MCP server 并完成握手 + 工具加载。
// 找不到配置或一个 server 都没连上时返回 nil——调用方据此只用内置工具继续跑。
func startMCP(ctx context.Context) *mcp.MCPManager {
	cfg, path, err := loadMCPConfig()
	if err != nil {
		fmt.Printf("\n未加载 MCP 配置(%v),仅测试内置工具。\n", err)
		return nil
	}
	fmt.Printf("\n加载 MCP 配置: %s (%d 个 server)\n", path, len(cfg.MCPServers))

	manager := mcp.NewMCPManager(cfg)
	manager.Initialize(ctx)

	// 一个 server 都没连上就没必要留着空 manager。
	snapshot := manager.StatusSnapshot()
	if len(snapshot.Servers) == 0 {
		fmt.Println("没有任何 MCP server 连接成功,仅测试内置工具。")
		_ = manager.Close()
		return nil
	}
	fmt.Println(mcp.FormatStatus(snapshot))
	return manager
}

// probeToolVisibility 验证 LLM 是否“看得见”内置工具和 MCP 工具。
// 先把两类工具分别列出来确认本地聚合成功,再带着合并后的工具集发一条
// 应当触发工具调用的请求,看模型是否回了 tool_use 块,并区分它选的是哪一类工具。
func probeToolVisibility(ctx context.Context, client llm.Client, model string, manager *mcp.MCPManager) {
	registry := tool.NewToolRegistry()
	registerWebTools(registry) // 把 web_search / web_fetch 接进同一个 registry
	builtinTools := registry.ToLLMTools()

	var mcpTools []llm.Tool
	if manager != nil {
		mcpTools = manager.AllLLMTools()
	}

	fmt.Printf("\n本地已注册 %d 个内置工具:\n", len(builtinTools))
	for _, t := range builtinTools {
		fmt.Printf("  - %s: %s\n", t.Name, t.Description)
	}

	fmt.Printf("\n聚合到 %d 个 MCP 工具:\n", len(mcpTools))
	for _, t := range mcpTools {
		fmt.Printf("  - %s: %s\n", t.Name, t.Description)
	}

	// 合并两类工具一起喂给模型——这正是要验证的:模型应同时看见内置和 MCP 工具。
	allTools := make([]llm.Tool, 0, len(builtinTools)+len(mcpTools))
	allTools = append(allTools, builtinTools...)
	allTools = append(allTools, mcpTools...)

	if len(allTools) == 0 {
		fmt.Println("\n没有任何工具可用,检查 builtin 包导入与 mcp.json 配置。")
		return
	}

	// 多轮探针,每轮一个问题,观察模型选了哪些工具:
	// 1) 中性问题——内置和 MCP 工具都能答,主要看模型会不会调工具。
	// 2) 浏览器问题——没有内置等价物,只能靠 chrome-devtools 的 MCP 工具。
	// 3) 搜索问题——需要实时/训练集外信息,诱导模型调 web_search。
	// 4) 抓取问题——给定具体 URL,诱导模型调 web_fetch。
	neutral := runProbe(ctx, client, model, allTools, "查看当前目录下有哪些文件。")

	var mcpNames []string
	if len(mcpTools) > 0 {
		mcpNames = runProbe(ctx, client, model, allTools,
			"打开一个浏览器标签页并导航到 https://example.com，然后截图。")
	}

	searchNames := runProbe(ctx, client, model, allTools,
		"帮我搜索一下 2025 年 Go 语言最新的稳定版本号是多少，并给出来源链接。")
	fetchNames := runProbe(ctx, client, model, allTools,
		"抓取网页 https://example.com 的内容，告诉我它的标题和大意。")

	hasWebSearch := registry.IsRegistered("web_search")

	fmt.Println("\n=== 结论 ===")
	fmt.Printf("内置工具可见且被调用      : %v\n", len(neutral) > 0)
	fmt.Printf("MCP 工具被调用            : %v\n", usedMCP(mcpNames))
	if hasWebSearch {
		fmt.Printf("web_search 被调用         : %v\n", usedTool(searchNames, "web_search"))
	} else {
		fmt.Println("web_search 被调用         : 跳过(未配置 SERPAPI_API_KEY)")
	}
	fmt.Printf("web_fetch 被调用          : %v\n", usedTool(fetchNames, "web_fetch"))

	if usedTool(searchNames, "web_search") || usedTool(fetchNames, "web_fetch") {
		fmt.Println("\n✅ 大模型能看见并调用 web_search / web_fetch。")
	} else {
		fmt.Println("\n⚠️  本轮模型未调用 web 工具(可能直接作答,或选了别的工具)。")
	}
}

// registerWebTools 把联网能力接进 registry。
// web_fetch 不依赖 key，总是注册；web_search 依赖 SerpAPI key，没配就跳过，
// 降级为少一个工具——和 NewWebSearchTool 注释里设想的接入方式一致。
func registerWebTools(r *tool.ToolRegistry) {
	if err := r.RegisterTool(builtin.NewWebFetchTool(web.NewHTTPFetcher())); err != nil {
		fmt.Fprintf(os.Stderr, "注册 web_fetch 失败: %v\n", err)
	}

	key := os.Getenv("SERPAPI_API_KEY")
	if key == "" {
		fmt.Println("未配置 SERPAPI_API_KEY，跳过 web_search 工具。")
		return
	}
	searcher := web.NewSerpApiSearcher(key)
	if err := r.RegisterTool(builtin.NewWebSearchTool(searcher)); err != nil {
		fmt.Fprintf(os.Stderr, "注册 web_search 失败: %v\n", err)
	}
}

// runProbe 带着 tools 发一条 question,打印响应,并返回模型本轮发起的工具调用名字列表
// (没调工具则为空切片)。调用方据此判断模型选了哪些工具。
func runProbe(ctx context.Context, client llm.Client, model string, tools []llm.Tool, question string) []string {
	fmt.Printf("\n发送问题(附带 %d 个工具): %s\n", len(tools), question)

	start := time.Now()
	resp, err := client.Chat(ctx, llm.ChatRequest{
		Model:     model,
		Messages:  []llm.Message{{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: question}}}},
		Tools:     tools,
		MaxTokens: 1024,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "调用失败: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("响应 (耗时 %s, stop=%s):\n", time.Since(start).Round(time.Millisecond), resp.StopReason)
	var used []string
	for _, b := range resp.Content {
		switch b.Type {
		case llm.ContentTypeText:
			if strings.TrimSpace(b.Text) != "" {
				fmt.Printf("  [text] %s\n", b.Text)
			}
		case llm.ContentTypeToolUse:
			used = append(used, b.ToolUse.Name)
			kind := "内置"
			// MCP 工具名带 mcp__ 前缀,据此判断模型选的是哪一类工具。
			if strings.HasPrefix(b.ToolUse.Name, mcp.KeyPrefix) {
				kind = "MCP"
			}
			fmt.Printf("  [tool_use:%s] name=%s input=%v\n", kind, b.ToolUse.Name, b.ToolUse.Input)
		}
	}
	return used
}

// usedMCP 判断本轮工具调用里是否有 MCP 工具(名字带 mcp__ 前缀)。
func usedMCP(names []string) bool {
	for _, n := range names {
		if strings.HasPrefix(n, mcp.KeyPrefix) {
			return true
		}
	}
	return false
}

// usedTool 判断本轮是否调用了指定名字的工具。
func usedTool(names []string, target string) bool {
	return slices.Contains(names, target)
}

// loadMCPConfig 找到并解析 mcp.json。
// 返回解析好的配置、实际命中的文件路径,以及可能的错误。
func loadMCPConfig() (*mcp.MCPConfig, string, error) {
	path, err := findMCPConfig()
	if err != nil {
		return nil, "", err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	var cfg mcp.MCPConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, "", fmt.Errorf("解析 %s 失败: %w", path, err)
	}
	return &cfg, path, nil
}

// findMCPConfig 从工作目录起向上查找 mcp.json。
// 既看当前目录的 mcp.json,也看 internal/mcp/mcp.json(本仓库实际存放位置)。
func findMCPConfig() (string, error) {
	candidates := []string{"mcp.json", filepath.Join("internal", "mcp", "mcp.json")}
	for _, rel := range candidates {
		if p, err := findUpwards(rel, 5); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("未找到 mcp.json")
}

// loadDotenv 从当前目录向上逐级查找 .env 并把键值写入进程环境。
// 已存在的环境变量不覆盖(命令行显式设置优先)。
func loadDotenv() error {
	path, err := findUpwards(".env", 5)
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.Trim(strings.TrimSpace(val), `"'`)
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, val)
		}
	}
	return sc.Err()
}

// findUpwards 从工作目录起向上最多 maxLevels 级寻找相对路径 rel。
func findUpwards(rel string, maxLevels int) (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for i := 0; i < maxLevels; i++ {
		p := filepath.Join(dir, rel)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("未找到 %s 文件", rel)
}
