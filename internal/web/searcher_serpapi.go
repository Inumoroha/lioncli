package web

import (
	"fmt"

	g "github.com/serpapi/google-search-results-golang"
)

type SerpApiSearcher struct {
	ApiKey string
}

func NewSerpApiSearcher(apiKey string) *SerpApiSearcher {
	return &SerpApiSearcher{
		ApiKey: apiKey,
	}
}

func (s *SerpApiSearcher) Name() string {
	return "SerpApiSearch"
}

func (s *SerpApiSearcher) IsAvailable() bool {
	return true
}

func (s *SerpApiSearcher) UnavailableHint() string {
	return ""
}

func (s *SerpApiSearcher) Search(query string, topK int) ([]SearchResult, error) {
	// 这里可以使用 SerpAPI 的 Go SDK 来实现搜索功能
	// 例如，构建搜索参数并调用 API 获取结果
	parameter := map[string]string{
		"engine": "google", // 搜索引擎类型 (SerpAPI支持bing, baidu等)
		"q":      query,    // 搜索关键词
		"hl":     "zh-cn",  // 语言：简体中文
		"gl":     "cn",     // 国家/地区：中国
		"num":    fmt.Sprintf("%d", topK),	 // 返回结果数量
	}

	// 初始化客户端
	search := g.NewGoogleSearch(parameter, s.ApiKey)

	// 发送请求并获取 JSON 结果
	results, err := search.GetJSON()
	if err != nil {
		return nil, fmt.Errorf("搜索失败: %v", err)
	}

	// 解析并提取我们需要的常规搜索结果 (Organic Results)
	organicResults, ok := results["organic_results"].([]any)
	if !ok {
		return nil, fmt.Errorf("没有找到常规搜索结果")
	}

	// 提取结果链接
	var targets []SearchResult
	for i, result := range organicResults {
		if i >= topK {
			break
		}

		// 全部用带 ok 的类型断言：SerpAPI 的单条结果常缺 snippet，偶尔整条
		// 不是对象，硬断言(.(string) / .(map))会在缺字段时 panic 掉整个进程。
		resMap, ok := result.(map[string]any)
		if !ok {
			continue
		}
		link, _ := resMap["link"].(string)
		if link == "" {
			// 没有链接的结果对下游 web_fetch 没有意义，跳过。
			continue
		}
		title, _ := resMap["title"].(string)
		description, _ := resMap["snippet"].(string)

		target := SearchResult{
			Title:       title,
			Description: description,
			URL:         link,
		}
		targets = append(targets, target)
	}

	return targets, nil
}
