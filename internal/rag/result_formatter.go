package rag

import (
	"fmt"
	"path/filepath"
	"strings"
)

func FormatForCLI(query string, results []SearchResult) string {
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("Found %d related code chunks:\n\n", len(results)))
	builder.WriteString(buildSummary(query, results))
	builder.WriteString("\n\n")
	for i, result := range results {
		builder.WriteString(fmt.Sprintf("%d. [%s:%s] (similarity: %.3f) %s\n",
			i+1, result.ChunkType, result.Name, result.Similarity, result.FilePath))
		builder.WriteString("   ")
		builder.WriteString(strings.ReplaceAll(buildSnippet(result.Content, 120), "\n", "\n   "))
		builder.WriteString("\n\n")
	}
	return strings.TrimSpace(builder.String())
}

func FormatForTool(query string, results []SearchResult) string {
	var builder strings.Builder
	builder.WriteString("Search summary:\n")
	builder.WriteString(buildSummary(query, results))
	builder.WriteString("\n\nSearch results:\n")
	for i, result := range results {
		builder.WriteString(fmt.Sprintf("%d. [%s:%s] (similarity: %.3f) %s\n",
			i+1, result.ChunkType, result.Name, result.Similarity, result.FilePath))
		builder.WriteString("   ")
		builder.WriteString(strings.ReplaceAll(buildSnippet(result.Content, 180), "\n", "\n   "))
		builder.WriteString("\n\n")
	}
	return strings.TrimSpace(builder.String())
}

func buildSummary(query string, results []SearchResult) string {
	if len(results) == 0 {
		return "No matching code chunks."
	}

	top := results[0]
	fileNames := orderedFileNames(results)
	queryTokens := TokenizeQuery(query)
	if len(queryTokens) > 3 {
		queryTokens = queryTokens[:3]
	}

	var builder strings.Builder
	builder.WriteString("- Top entry: [")
	builder.WriteString(top.ChunkType)
	builder.WriteByte(':')
	builder.WriteString(top.Name)
	builder.WriteString("] in ")
	builder.WriteString(shortenPath(top.FilePath))
	builder.WriteString(".\n- Related files: ")
	builder.WriteString(strings.Join(fileNames, ", "))
	builder.WriteString(".\n- Ranking considered ")
	if len(queryTokens) == 0 {
		builder.WriteString("semantic similarity.")
	} else {
		builder.WriteString(strings.Join(queryTokens, ", "))
		builder.WriteString(" and semantic similarity.")
	}
	return builder.String()
}

func orderedFileNames(results []SearchResult) []string {
	seen := make(map[string]struct{})
	var names []string
	for _, result := range results {
		base := filepath.Base(result.FilePath)
		if _, ok := seen[base]; ok {
			continue
		}
		seen[base] = struct{}{}
		names = append(names, base)
		if len(names) >= 3 {
			break
		}
	}
	return names
}

func buildSnippet(content string, maxChars int) string {
	normalized := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(content, "\r\n", "\n"), "\r", "\n"))
	if normalized == "" {
		return "(empty chunk)"
	}
	runes := []rune(normalized)
	if len(runes) <= maxChars {
		return normalized
	}
	return string(runes[:maxChars]) + "..."
}

func shortenPath(filePath string) string {
	clean := filepath.Clean(filePath)
	parts := strings.Split(clean, string(filepath.Separator))
	if len(parts) <= 3 {
		return clean
	}
	return filepath.Join(parts[len(parts)-3:]...)
}
