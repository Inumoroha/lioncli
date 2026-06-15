package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"lioncli/internal/agent"
	"lioncli/internal/llm"
	"lioncli/internal/llm/openai"
	"lioncli/internal/mcp"
	"lioncli/internal/memory"
	"lioncli/internal/plan"
	"lioncli/internal/prompt"
	"lioncli/internal/rag"
	runtimeapi "lioncli/internal/runtime/api"
	"lioncli/internal/skill"
	"lioncli/internal/tool"
	"lioncli/internal/tool/builtin"
	"lioncli/internal/tui"
	"lioncli/internal/web"
)

func main() {
	loadDotEnv()

	client, modelName := newLLMClient()

	registry := tool.NewToolRegistry()

	skillStore := skill.NewSkillStateStore(skillsStatePath())
	skillReg := skill.NewSkillRegistry(skill.BuiltinFS(), skill.BuiltinRoot(), "", "", skillStore)
	skillReg.Reload()
	for _, w := range skillReg.Warnings() {
		fmt.Fprintf(os.Stderr, "warn: skill: %s\n", w)
	}
	skillBuf := skill.NewSkillContextBuffer()
	if err := registry.RegisterTool(builtin.NewLoadSkillTool(skillReg, skillBuf)); err != nil {
		fmt.Fprintf(os.Stderr, "warn: register load_skill: %v\n", err)
	}

	if key := strings.TrimSpace(os.Getenv("SERPAPI_API_KEY")); key != "" {
		if err := registry.RegisterTool(builtin.NewWebSearchTool(web.NewSerpApiSearcher(key))); err != nil {
			fmt.Fprintf(os.Stderr, "warn: register web_search: %v\n", err)
		}
	}

	if err := registry.RegisterTool(builtin.NewWebFetchTool(web.NewHTTPFetcher())); err != nil {
		fmt.Fprintf(os.Stderr, "warn: register web_fetch: %v\n", err)
	}

	var mcpStatus func() mcp.StatusSnapshot
	if mgr := tryStartMCP(mcpConfigPath()); mgr != nil {
		defer mgr.Close()
		mcpStatus = mgr.StatusSnapshot
		if err := registry.RegisterMCP(mgr); err != nil {
			fmt.Fprintf(os.Stderr, "warn: register mcp tools: %v\n", err)
		}
		if err := tool.RegisterMCPHelpers(registry, mgr); err != nil {
			fmt.Fprintf(os.Stderr, "warn: register mcp helpers: %v\n", err)
		}
		mgr.OnToolsChanged(func() {
			if err := registry.SyncMCP(mgr); err != nil {
				fmt.Fprintf(os.Stderr, "warn: sync mcp tools: %v\n", err)
			}
		})
	}

	assembler := prompt.NewDefaultAssembler()

	memAdapter := memory.NewLLMChatAdapter(client, modelName)
	memMgr, err := memory.NewMemoryManager(memAdapter)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warn: initialize memory subsystem failed; memory disabled: %v\n", err)
		memMgr = nil
	}

	planAdapter := plan.NewLLMChatAdapter(client, modelName)
	planner := plan.NewPlanner(planAdapter, nil)

	embedder := rag.NewEmbeddingClientFromEnv()
	projectRoot, err := os.Getwd()
	if err != nil {
		projectRoot = "."
	}
	if err := registry.RegisterTool(newCodeSearchTool(projectRoot, embedder)); err != nil {
		fmt.Fprintf(os.Stderr, "warn: register code_search: %v\n", err)
	}

	a := agent.New(client, registry, modelName, skillReg, skillBuf, assembler, memMgr, planner, embedder)
	if closer := startRuntimeAPI(a); closer != nil {
		defer closer()
	}

	if err := tui.Run(a, modelName, mcpStatus); err != nil {
		fmt.Fprintf(os.Stderr, "tui exited: %v\n", err)
		os.Exit(1)
	}
}

func newLLMClient() (llm.Client, string) {
	if envEnabled("TEACLI_DEMO_MODE") {
		return demoClient{}, "teacli-demo"
	}

	apiKey := os.Getenv("AI_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "error: AI_API_KEY is not set")
		fmt.Fprintln(os.Stderr, "hint: set AI_API_KEY in your environment or .env file, or set TEACLI_DEMO_MODE=true")
		os.Exit(1)
	}
	baseURL := getenvOr("AI_BASE_URL", "https://api.openai.com/v1/chat/completions")
	modelName := getenvOr("AI_MODEL", "deepseek-chat")

	return openai.New(apiKey,
		openai.WithBaseURL(baseURL),
		openai.WithDefaultModel(modelName),
	), modelName
}

type demoClient struct{}

func (demoClient) Chat(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	var text strings.Builder
	text.WriteString("Demo mode is running locally. Configure DEEPSEEK_API_KEY to use a real model.")
	if len(req.Messages) > 0 {
		last := req.Messages[len(req.Messages)-1]
		if content := extractText(last.Content); strings.TrimSpace(content) != "" {
			text.WriteString("\n\nYou said: ")
			text.WriteString(content)
		}
	}
	return &llm.ChatResponse{
		Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: text.String()}},
		StopReason: llm.StopReasonEndTurn,
	}, nil
}

func extractText(blocks []llm.ContentBlock) string {
	var b strings.Builder
	for _, blk := range blocks {
		if blk.Type == llm.ContentTypeText {
			b.WriteString(blk.Text)
		}
	}
	return b.String()
}

func newCodeSearchTool(projectRoot string, embedder rag.Embedder) tool.Tool {
	return tool.Tool{
		Name:        "code_search",
		Description: "Search the indexed project code using hybrid semantic and keyword retrieval.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Natural language query or code keyword to search for.",
				},
				"top_k": map[string]any{
					"type":        "integer",
					"description": "Maximum number of results to return.",
					"default":     5,
				},
			},
			"required": []string{"query"},
		},
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			query := strings.TrimSpace(tool.StringArg(args, "query"))
			if query == "" {
				return "", fmt.Errorf("missing required parameter: query")
			}
			topK := tool.IntArg(args, "top_k", 5)
			if topK <= 0 {
				topK = 5
			}
			if topK > 20 {
				topK = 20
			}
			retriever, err := rag.NewCodeRetriever(projectRoot, embedder)
			if err != nil {
				return "", err
			}
			defer retriever.Close()
			results, err := retriever.HybridSearch(ctx, query, topK)
			if err != nil {
				return "", err
			}
			return rag.FormatForTool(query, results), nil
		},
	}
}

func loadDotEnv() {
	dir, err := os.Getwd()
	if err != nil {
		return
	}
	for {
		candidate := filepath.Join(dir, ".env")
		if _, statErr := os.Stat(candidate); statErr == nil {
			if loadErr := godotenv.Load(candidate); loadErr != nil {
				fmt.Fprintf(os.Stderr, "warn: load .env failed %s: %v\n", candidate, loadErr)
			}
			return
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return
		}
		dir = parent
	}
}

func getenvOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envEnabled(key string) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	return strings.EqualFold(raw, "1") || strings.EqualFold(raw, "true") || strings.EqualFold(raw, "yes")
}

func skillsStatePath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "teacli", "skills.json")
}

func mcpConfigPath() string {
	if p := os.Getenv("TEACLI_MCP_CONFIG"); p != "" {
		return p
	}
	const rel = "internal/mcp/mcp.json"
	dir, err := os.Getwd()
	if err != nil {
		return rel
	}
	for {
		candidate := filepath.Join(dir, rel)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func tryStartMCP(path string) *mcp.MCPManager {
	if path == "" {
		fmt.Fprintln(os.Stderr, "warn: MCP config internal/mcp/mcp.json was not found; MCP tools disabled. Set TEACLI_MCP_CONFIG to override.")
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "warn: MCP config does not exist: %s; MCP tools disabled\n", path)
			return nil
		}
		fmt.Fprintf(os.Stderr, "warn: read mcp config: %v\n", err)
		return nil
	}
	var cfg mcp.MCPConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		fmt.Fprintf(os.Stderr, "warn: parse mcp config: %v\n", err)
		return nil
	}
	fmt.Fprintf(os.Stderr, "info: using MCP config %s\n", path)
	mgr := mcp.NewMCPManager(&cfg)
	mgr.Initialize(context.Background())
	return mgr
}

func startRuntimeAPI(a *agent.Agent) func() {
	if !envEnabled("TEACLI_RUNTIME_API_ENABLED") && !envEnabled("PAICLI_RUNTIME_API_ENABLED") {
		return nil
	}
	store, err := runtimeapi.NewThreadStore("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "warn: runtime API disabled: initialize store failed: %v\n", err)
		return nil
	}
	server, err := runtimeapi.NewServer(store, func(ctx context.Context, input string) (string, error) {
		return a.Run(ctx, input)
	}, getenvOr("TEACLI_RUNTIME_API_ADDR", "127.0.0.1:0"), runtimeapi.ConfiguredAPIKey())
	if err != nil {
		fmt.Fprintf(os.Stderr, "warn: runtime API disabled: %v\n", err)
		return nil
	}
	addr, err := server.Start()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warn: runtime API disabled: start failed: %v\n", err)
		return nil
	}
	fmt.Fprintf(os.Stderr, "info: runtime API listening on http://%s\n", addr)
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Close(ctx)
	}
}
