package web

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// EmptyContentHint 在抽不到正文时返回。常见于 JS 渲染的 SPA、或被 Cloudflare 等
// 反爬墙挡住的页面。提示里点明这是已知边界，让 Agent 别反复重试浪费 token。
const EmptyContentHint = "未提取到正文。可能是 JS 渲染或防爬墙；本期范围内不再重试。"

// Extractor 把原始 HTML 抽成只含正文的 Markdown。
type Extractor interface {
	Extract(rawHTML string) string
}

// HTMLExtractor 去掉导航/广告/页脚等噪声，定位主语义容器（找不到就打分兜底），
// 再把正文转成 Markdown。
type HTMLExtractor struct{}

func NewHTMLExtractor() *HTMLExtractor { return &HTMLExtractor{} }

// 第一步：直接删掉的噪声标签。
var noiseTags = map[string]bool{
	"script": true, "style": true, "nav": true, "aside": true,
	"footer": true, "header": true, "form": true, "iframe": true,
}

// 第一步：class/id 命中这些关键词的元素也清掉。
var noiseKeywords = []string{"ads", "banner", "sidebar", "comment"}

// 第三步：打分兜底时参与打分的 block 容器标签。
var blockCandidates = map[string]bool{
	"div": true, "section": true, "article": true, "main": true,
	"td": true, "table": true, "ul": true, "ol": true, "blockquote": true,
}

// Extract 走完「清噪声 → 找语义容器 → 打分兜底 → 转 Markdown」四步。
func (e *HTMLExtractor) Extract(rawHTML string) string {
	doc, err := html.Parse(strings.NewReader(rawHTML))
	if err != nil {
		return EmptyContentHint
	}

	cleanNoise(doc) // 第一步

	container := findSemanticMain(doc) // 第二步
	if container == nil {
		container = bestByScore(doc) // 第三步
	}
	if container == nil {
		// 仍找不到就退到 <body>，让短页面也能抽出来；真为空时下面会兜到 hint。
		if body := firstElement(doc, "body"); body != nil {
			container = body
		} else {
			container = doc
		}
	}

	md := cleanupMarkdown(renderChildren(container)) // 第四步
	if strings.TrimSpace(md) == "" {
		return EmptyContentHint
	}
	return md
}

// ---------- 第一步：清理噪声 ----------

func cleanNoise(root *html.Node) {
	var toRemove []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode && isNoise(c) {
				toRemove = append(toRemove, c)
				continue // 整棵子树一起删，不再深入
			}
			walk(c)
		}
	}
	walk(root)
	for _, n := range toRemove {
		if n.Parent != nil {
			n.Parent.RemoveChild(n)
		}
	}
}

func isNoise(n *html.Node) bool {
	if noiseTags[n.Data] {
		return true
	}
	classID := strings.ToLower(getAttr(n, "class") + " " + getAttr(n, "id"))
	for _, kw := range noiseKeywords {
		if strings.Contains(classID, kw) {
			return true
		}
	}
	return false
}

// ---------- 第二步：找语义主容器 ----------

// findSemanticMain 收集 <article>、<main>、[role=main]，按打分取最像正文的那个；
// 一个都没有则返回 nil。
func findSemanticMain(root *html.Node) *html.Node {
	var candidates []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if n.Data == "article" || n.Data == "main" || strings.EqualFold(getAttr(n, "role"), "main") {
				candidates = append(candidates, n)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)

	var best *html.Node
	bestScore := -1.0
	for _, c := range candidates {
		if s := score(c); s > bestScore {
			bestScore, best = s, c
		}
	}
	return best
}

// ---------- 第三步：打分兜底 ----------

// bestByScore 给所有 block 容器打分，取最高分者；最高分仍为 0 则返回 nil。
func bestByScore(root *html.Node) *html.Node {
	var best *html.Node
	bestScore := 0.0
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && blockCandidates[n.Data] {
			if s := score(n); s > bestScore {
				bestScore, best = s, n
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return best
}

// score 复刻规格里的打分公式：文本长度 ×(1 - 链接密度惩罚)。
// 思路：导航/侧边栏链接密度高，正文文字多链接少；用链接密度做惩罚区分二者。
// 长度按字符数（rune）计，对中文更公平。
func score(el *html.Node) float64 {
	text := strings.TrimSpace(nodeText(el))
	textLen := utf8.RuneCountInString(text)
	if textLen < 80 {
		return 0
	}
	linkLen := 0
	for _, a := range selectAll(el, "a") {
		linkLen += utf8.RuneCountInString(nodeText(a))
	}
	linkRatio := float64(linkLen) / float64(textLen)
	penalty := math.Min(linkRatio*2.0, 1.0)
	return float64(textLen) * (1.0 - penalty)
}

// ---------- 第四步：转 Markdown ----------

func renderChildren(n *html.Node) string {
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(toMarkdown(c))
	}
	return b.String()
}

func toMarkdown(n *html.Node) string {
	switch n.Type {
	case html.TextNode:
		return collapseSpaces(n.Data)
	case html.ElementNode:
		return renderElement(n)
	default:
		return renderChildren(n)
	}
}

func renderElement(n *html.Node) string {
	switch n.Data {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		level := int(n.Data[1] - '0')
		return "\n\n" + strings.Repeat("#", level) + " " + inlineText(n) + "\n\n"
	case "p", "div", "section", "article", "main":
		// 块级：前后留空行；div/section 用作通用块，递归其内容。
		inner := strings.TrimSpace(renderChildren(n))
		if inner == "" {
			return ""
		}
		return "\n\n" + inner + "\n\n"
	case "br":
		return "\n"
	case "strong", "b":
		return "**" + strings.TrimSpace(renderChildren(n)) + "**"
	case "em", "i":
		return "*" + strings.TrimSpace(renderChildren(n)) + "*"
	case "a":
		txt := strings.TrimSpace(renderChildren(n))
		href := getAttr(n, "href")
		switch {
		case txt == "":
			return ""
		case href == "":
			return txt
		default:
			return "[" + txt + "](" + href + ")"
		}
	case "pre":
		return "\n\n```\n" + strings.Trim(nodeText(n), "\n") + "\n```\n\n"
	case "code":
		if n.Parent != nil && n.Parent.Data == "pre" {
			return nodeText(n) // 由外层 pre 统一包围
		}
		return "`" + strings.TrimSpace(nodeText(n)) + "`"
	case "ul", "ol":
		return renderList(n)
	case "blockquote":
		inner := strings.TrimSpace(renderChildren(n))
		var b strings.Builder
		b.WriteString("\n\n")
		for line := range strings.SplitSeq(inner, "\n") {
			b.WriteString("> ")
			b.WriteString(line)
			b.WriteByte('\n')
		}
		b.WriteString("\n")
		return b.String()
	case "table":
		return renderTable(n)
	default:
		return renderChildren(n)
	}
}

func renderList(n *html.Node) string {
	ordered := n.Data == "ol"
	var b strings.Builder
	b.WriteString("\n")
	idx := 1
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode || c.Data != "li" {
			continue
		}
		marker := "- "
		if ordered {
			marker = fmt.Sprintf("%d. ", idx)
			idx++
		}
		line := strings.ReplaceAll(strings.TrimSpace(renderChildren(c)), "\n", " ")
		b.WriteString(marker)
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteString("\n")
	return b.String()
}

func renderTable(n *html.Node) string {
	var rows [][]string
	var collect func(*html.Node)
	collect = func(node *html.Node) {
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode && c.Data == "tr" {
				var cells []string
				for d := c.FirstChild; d != nil; d = d.NextSibling {
					if d.Type == html.ElementNode && (d.Data == "td" || d.Data == "th") {
						cell := strings.ReplaceAll(inlineText(d), "|", "\\|")
						cells = append(cells, cell)
					}
				}
				if len(cells) > 0 {
					rows = append(rows, cells)
				}
			} else {
				collect(c)
			}
		}
	}
	collect(n)
	if len(rows) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n\n")
	header := rows[0]
	fmt.Fprintf(&b, "| %s |\n", strings.Join(header, " | "))
	sep := make([]string, len(header))
	for i := range sep {
		sep[i] = "---"
	}
	fmt.Fprintf(&b, "| %s |\n", strings.Join(sep, " | "))
	for _, r := range rows[1:] {
		fmt.Fprintf(&b, "| %s |\n", strings.Join(r, " | "))
	}
	b.WriteString("\n")
	return b.String()
}

// ---------- 工具函数 ----------

func getAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// firstElement 深度优先返回第一个指定标签的元素。
func firstElement(root *html.Node, tag string) *html.Node {
	var found *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if found != nil {
			return
		}
		if n.Type == html.ElementNode && n.Data == tag {
			found = n
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return found
}

// selectAll 返回 root 子树下所有指定标签的元素。
func selectAll(root *html.Node, tag string) []*html.Node {
	var out []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == tag {
			out = append(out, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return out
}

// nodeText 返回子树拼接的纯文本。
func nodeText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			b.WriteString(node.Data)
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

// inlineText 把子树渲染成单行内联文本（用于标题、表格单元格）。
func inlineText(n *html.Node) string {
	s := strings.ReplaceAll(renderChildren(n), "\n", " ")
	return strings.TrimSpace(collapseSpaces(s))
}

// collapseSpaces 把连续空白压成单个空格，保留首尾的单个空格以维持内联词间距。
func collapseSpaces(s string) string {
	var b strings.Builder
	prevSpace := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		b.WriteRune(r)
		prevSpace = false
	}
	return b.String()
}

var (
	reBlankLines  = regexp.MustCompile(`\n{3,}`)
	reTrailingTab = regexp.MustCompile(`[ \t]+\n`)
)

func cleanupMarkdown(s string) string {
	s = reTrailingTab.ReplaceAllString(s, "\n")
	s = reBlankLines.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}
