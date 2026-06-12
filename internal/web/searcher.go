package web

// Searcher 是一个搜索工具的接口。
type Searcher interface {
	Name() string
	IsAvailable() bool
	UnavailableHint() string
	Search(query string, topK int) ([]SearchResult, error)
}

// SearchResult 是一个搜索结果。
type SearchResult struct {
	Title       string
	Description string
	URL         string
}