package rag

import (
	"regexp"
	"strings"
	"unicode"

	"lioncli/internal/segment"
)

var asciiTokenPattern = regexp.MustCompile(`[A-Za-z][A-Za-z0-9_.$-]+`)

var stopwords = map[string]struct{}{
	"怎么": {},
	"如何": {},
	"什么": {},
	"哪些": {},
	"一下": {},
	"实现": {},
	"的是": {},
	"一个": {},
	"可以": {},
	"这里": {},
	"那里": {},
}

// TokenizeQuery keeps code-like tokens from a natural-language question.
func TokenizeQuery(query string) []string {
	normalized := strings.TrimSpace(query)
	if normalized == "" {
		return nil
	}

	seen := make(map[string]struct{})
	var tokens []string
	add := func(token string) {
		token = strings.TrimSpace(token)
		if !isUsefulToken(token) {
			return
		}
		if _, ok := seen[token]; ok {
			return
		}
		seen[token] = struct{}{}
		tokens = append(tokens, token)
	}

	for _, token := range strings.FieldsFunc(normalized, func(r rune) bool {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.In(r, unicode.Han) {
			return false
		}
		switch r {
		case '_', '.', '$', '-':
			return false
		default:
			return true
		}
	}) {
		add(token)
		// 中文整块再用 gse 切词,提升召回("登录功能" → "登录"/"功能");原整块也保留,
		// 兼顾短语精确匹配。分词器未就绪时 Cut 返回 nil,自然只剩整块 token。
		if segment.HasHan(token) {
			for _, w := range segment.Cut(token) {
				add(w)
			}
		}
	}

	for _, token := range asciiTokenPattern.FindAllString(normalized, -1) {
		add(token)
	}

	return tokens
}

func isUsefulToken(token string) bool {
	if token == "" {
		return false
	}
	if len([]rune(token)) < 2 {
		return false
	}
	if _, ok := stopwords[strings.ToLower(token)]; ok {
		return false
	}
	return isMeaningfulToken(token)
}

func isMeaningfulToken(token string) bool {
	hasHan := false
	hasWord := false
	for _, r := range token {
		if unicode.In(r, unicode.Han) {
			hasHan = true
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			hasWord = true
		}
	}
	return hasHan || hasWord
}
