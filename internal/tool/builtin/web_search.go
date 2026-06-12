package builtin

import (
	"context"
	"fmt"
	"strings"

	"lioncli/internal/tool"
	"lioncli/internal/web"
)

// NewWebSearchTool 把一个 web.Searcher 包装成可被 LLM 调用的 web_search 工具。
// 不走 init()+RegisterAuto：搜索依赖外部 API key，由 main.go 在拿到可用的
// Searcher 后通过 registry.Register 接入；没配 key 时直接不注册，降级为少一个工具。
func NewWebSearchTool(searcher web.Searcher) tool.Tool {
	return tool.Tool{
		Name: "web_search",
		Description: "Search the public web for up-to-date information. " +
			"Returns a ranked list of results with title, URL, and snippet. " +
			"Use this when the answer may depend on current events or facts not in the model's training data.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "The search query.",
				},
				"top_k": map[string]any{
					"type":        "integer",
					"description": "Maximum number of results to return (default 5).",
				},
			},
			"required": []string{"query"},
		},
		Execute: func(_ context.Context, args map[string]any) (string, error) {
			query := tool.StringArg(args, "query")
			if query == "" {
				return "", fmt.Errorf("missing required parameter: query")
			}
			if !searcher.IsAvailable() {
				// 不可用时返回提示而不是报错，让 LLM 能据此换个策略。
				return searcher.UnavailableHint(), nil
			}

			topK := tool.IntArg(args, "top_k", 5)
			if topK <= 0 {
				topK = 5
			}

			results, err := searcher.Search(query, topK)
			if err != nil {
				// 把错误交给 LLM，而不是中断对话循环，和其它本地工具保持一致。
				return fmt.Sprintf("web search failed for %q: %v", query, err), nil
			}
			return formatSearchResults(query, results), nil
		},
	}
}

// formatSearchResults 把结构化结果拼成给 LLM 阅读的纯文本。
func formatSearchResults(query string, results []web.SearchResult) string {
	if len(results) == 0 {
		return fmt.Sprintf("No results found for %q.", query)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Search results for %q:\n", query)
	for i, r := range results {
		fmt.Fprintf(&b, "\n%d. %s\n   %s\n", i+1, r.Title, r.URL)
		if r.Description != "" {
			fmt.Fprintf(&b, "   %s\n", r.Description)
		}
	}
	return b.String()
}
