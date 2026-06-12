package skill

import (
	"strings"
	"unicode/utf8"
)

// ParseResult 是 SKILL.md 解析结果:frontmatter 键值 + 正文 + 告警。
type ParseResult struct {
	Frontmatter map[string]any
	Body        string
	Warnings    []string
}

// ParseSkillFrontmatter 解析 SKILL.md 全文:开头 ---\n ... \n--- 之间是
// frontmatter,其余是正文。支持标量、[a, b] 列表、| 多行块标量;不引 YAML 依赖。
func ParseSkillFrontmatter(fullText string) ParseResult {
	if fullText == "" {
		return ParseResult{
			Frontmatter: map[string]any{},
			Body:        "",
			Warnings:    []string{"SKILL.md 内容为 null 或空"},
		}
	}

	normalized := strings.ReplaceAll(fullText, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	// 去掉可能的 UTF-8 BOM,确保 --- 起始标记能被识别。
	normalized = strings.TrimPrefix(normalized, "\xef\xbb\xbf")

	if !strings.HasPrefix(normalized, "---\n") {
		return ParseResult{
			Frontmatter: map[string]any{},
			Body:        normalized,
			Warnings:    []string{"缺少 frontmatter 起始标记 ---"},
		}
	}

	endIdx := findFrontmatterEnd(normalized)
	if endIdx < 0 {
		return ParseResult{
			Frontmatter: map[string]any{},
			Body:        normalized,
			Warnings:    []string{"缺少 frontmatter 结束标记 ---"},
		}
	}

	frontmatterText := normalized[4:endIdx]
	body := normalized[endIdx+4:]
	body = strings.TrimPrefix(body, "\n")

	warnings := make([]string, 0)
	frontmatter := parseFrontmatter(frontmatterText, &warnings)
	return ParseResult{
		Frontmatter: frontmatter,
		Body:        body,
		Warnings:    warnings,
	}
}

func findFrontmatterEnd(text string) int {
	idx := 4
	for idx < len(text) {
		lineEnd := strings.IndexByte(text[idx:], '\n')
		if lineEnd < 0 {
			return -1
		}
		lineEnd += idx
		line := text[idx:lineEnd]
		if line == "---" {
			return idx
		}
		idx = lineEnd + 1
	}
	return -1
}

func parseFrontmatter(text string, warnings *[]string) map[string]any {
	result := make(map[string]any)
	lines := strings.Split(text, "\n")

	for i := 0; i < len(lines); {
		line := lines[i]
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			i++
			continue
		}

		colonIdx := findKeyColonIndex(line)
		if colonIdx < 0 {
			*warnings = append(*warnings, "无法解析的 frontmatter 行: "+line)
			i++
			continue
		}

		key := strings.TrimSpace(line[:colonIdx])
		rawValue := strings.TrimSpace(line[colonIdx+1:])
		if key == "" {
			*warnings = append(*warnings, "frontmatter 行缺少 key: "+line)
			i++
			continue
		}
		if rawValue == "" {
			*warnings = append(*warnings, "frontmatter 字段 '"+key+"' 缺少值或使用了不支持的嵌套结构")
			i++
			continue
		}
		if strings.HasPrefix(rawValue, "{") {
			*warnings = append(*warnings, "frontmatter 字段 '"+key+"' 使用了不支持的嵌套对象语法")
			i++
			continue
		}

		if rawValue == "|" || strings.HasPrefix(rawValue, "|") {
			var builder strings.Builder
			i++
			baseIndent := -1
			for i < len(lines) {
				next := lines[i]
				if strings.TrimSpace(next) == "" {
					builder.WriteByte('\n')
					i++
					continue
				}

				indent := leadingSpaces(next)
				if indent == 0 {
					break
				}
				if baseIndent < 0 {
					baseIndent = indent
				}
				if indent < baseIndent {
					break
				}

				builder.WriteString(next[baseIndent:])
				builder.WriteByte('\n')
				i++
			}
			result[key] = collapseWhitespace(builder.String())
			continue
		}

		if strings.HasPrefix(rawValue, "[") && strings.HasSuffix(rawValue, "]") {
			inner := strings.TrimSpace(rawValue[1 : len(rawValue)-1])
			items := make([]string, 0)
			if inner != "" {
				for _, part := range strings.Split(inner, ",") {
					trimmed := strings.TrimSpace(part)
					trimmed = trimQuotes(trimmed)
					if trimmed != "" {
						items = append(items, trimmed)
					}
				}
			}
			result[key] = items
			i++
			continue
		}

		result[key] = trimQuotes(rawValue)
		i++
	}

	return result
}

func findKeyColonIndex(line string) int {
	inSingle := false
	inDouble := false

	for i, r := range line {
		switch r {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case ':':
			if !inSingle && !inDouble {
				return i
			}
		}
	}

	return -1
}

func leadingSpaces(s string) int {
	count := 0
	for _, r := range s {
		if r != ' ' {
			break
		}
		count += utf8.RuneLen(r)
	}
	return count
}

func trimQuotes(s string) string {
	if len(s) >= 2 {
		if (strings.HasPrefix(s, "\"") && strings.HasSuffix(s, "\"")) ||
			(strings.HasPrefix(s, "'") && strings.HasSuffix(s, "'")) {
			return s[1 : len(s)-1]
		}
	}
	return s
}

func collapseWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
