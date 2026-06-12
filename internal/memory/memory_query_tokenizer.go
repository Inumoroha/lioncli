package memory

import (
	"strings"
	"unicode"

	"lioncli/internal/segment"
)

func TokenizeMemoryQuery(query string) []string {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil
	}

	seen := map[string]struct{}{}
	var tokens []string
	add := func(part string) {
		part = strings.TrimSpace(part)
		if len([]rune(part)) < 2 || isPunctuationOnly(part) {
			return
		}
		if _, ok := seen[part]; ok {
			return
		}
		seen[part] = struct{}{}
		tokens = append(tokens, part)
	}

	for _, part := range strings.FieldsFunc(query, func(r rune) bool {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Han, r) {
			return false
		}
		switch r {
		case '_', '-', '.':
			return false
		default:
			return true
		}
	}) {
		add(part)
		// 中文整块再用 gse 切词,提升召回;原整块也保留。分词器未就绪时 Cut 返回 nil。
		if segment.HasHan(part) {
			for _, w := range segment.Cut(part) {
				add(w)
			}
		}
	}
	return tokens
}

func MemoryTextMatches(text string, queryTokens []string) bool {
	if strings.TrimSpace(text) == "" || len(queryTokens) == 0 {
		return false
	}
	normalized := strings.ToLower(text)
	for _, token := range queryTokens {
		if token != "" && strings.Contains(normalized, token) {
			return true
		}
	}
	return false
}

func isPunctuationOnly(value string) bool {
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Han, r) {
			return false
		}
	}
	return true
}
