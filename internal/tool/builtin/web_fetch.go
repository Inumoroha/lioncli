package builtin

import (
	"context"
	"fmt"
	"strings"

	"lioncli/internal/tool"
	"lioncli/internal/web"
)

// maxFetchToolOutput 返回给 LLM 的正文上限。抓取层最大可读 5MB，但塞进对话
// 上下文不现实，这里和 read_file 一样截断，避免一次抓取把上下文撑爆。
const maxFetchToolOutput = 20000

// NewWebFetchTool 把一个 web.Fetcher 包装成可被 LLM 调用的 web_fetch 工具。
// 抓取不依赖外部 key，可直接由 main.go 注册。导出供包外（main）构造注入。
func NewWebFetchTool(fetcher web.Fetcher) tool.Tool {
	// HTML 页面抽正文转 Markdown 再返回，省 token；非 HTML 原样返回。
	extractor := web.NewHTMLExtractor()
	return tool.Tool{
		Name: "web_fetch",
		Description: "Fetch a web page via HTTP GET. For HTML pages the main content is " +
			"extracted and converted to Markdown (navigation, ads and boilerplate stripped); " +
			"non-HTML responses are returned as-is. Includes status code, content type and charset. " +
			"The body is capped at 5MB and the request times out after 30s. " +
			"Use this to read a specific URL, e.g. a link returned by web_search.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{
					"type":        "string",
					"description": "The absolute http(s) URL to fetch.",
				},
			},
			"required": []string{"url"},
		},
		Execute: func(_ context.Context, args map[string]any) (string, error) {
			url := tool.StringArg(args, "url")
			if url == "" {
				return "", fmt.Errorf("missing required parameter: url")
			}

			resp, err := fetcher.Fetch(url)
			if err != nil {
				// 抓取出错时把原因交给 LLM，而不是中断对话循环。
				return fmt.Sprintf("web fetch failed for %q: %v", url, err), nil
			}
			return formatRawResponse(resp, extractor), nil
		},
	}
}

// formatRawResponse 把抓取结果拼成给 LLM 阅读的文本：先一行元信息，再正文。
// HTML 走抽取器转 Markdown，其它类型（JSON、纯文本等）原样输出。
func formatRawResponse(r *web.RawResponse, extractor web.Extractor) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Fetched %s\n", r.URL)
	fmt.Fprintf(&b, "Status: %d | Content-Type: %s | Charset: %s",
		r.StatusCode, r.ContentType, r.Charset)
	if r.Truncated {
		b.WriteString(" | [body truncated at 5MB during fetch]")
	}
	b.WriteString("\n\n")

	content := r.Body
	if isHTML(r.ContentType) {
		content = extractor.Extract(r.Body)
	}
	if len(content) > maxFetchToolOutput {
		content = content[:maxFetchToolOutput] + "\n\n...[truncated]..."
	}
	b.WriteString(content)
	return b.String()
}

func isHTML(contentType string) bool {
	return strings.Contains(strings.ToLower(contentType), "html")
}
