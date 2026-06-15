package prompt

import (
	"fmt"
	"regexp"
	"strings"
)

// placeholderRe 匹配模式片段里的 {{key}} 占位符。
var placeholderRe = regexp.MustCompile(`\{\{([a-zA-Z0-9_]+)\}\}`)

// blankLinesRe 用于把删行后残留的连续空行折叠成一个。
var blankLinesRe = regexp.MustCompile(`\n{3,}`)

// Assembler 按固定顺序把各片段拼装成完整 system prompt,对齐 Java PromptAssembler。
type Assembler struct {
	repo *Repository
}

// NewAssembler 用给定 Repository 构造 Assembler。
func NewAssembler(repo *Repository) *Assembler {
	return &Assembler{repo: repo}
}

// Assemble 装配 mode + ctx 对应的完整提示词。装配顺序:
// base → personalities/calm → 模式片段(做 {{变量}} 替换)→ approvals/<mode> →
// 动态「## Project Context」→ 动态「## Skills」→ context/context-management → handoff。
// 任一必需片段缺失、或结果不含「## Language」段,返回 error。
func (a *Assembler) Assemble(mode Mode, ctx Context) (string, error) {
	base, err := a.repo.LoadRequired("base.md")
	if err != nil {
		return "", err
	}
	if err := validateLanguage(base, "base.md"); err != nil {
		return "", err
	}

	calm, err := a.repo.LoadRequired("personalities/calm.md")
	if err != nil {
		return "", err
	}

	modePath := mode.resourcePath()
	if modePath == "" {
		return "", fmt.Errorf("unknown prompt mode: %s", mode)
	}
	modeBody, err := a.repo.LoadRequired(modePath)
	if err != nil {
		return "", err
	}

	approval, err := a.repo.LoadRequired("approvals/" + approvalMode(ctx) + ".md")
	if err != nil {
		return "", err
	}

	ctxMgmt, err := a.repo.LoadRequired("context/context-management.md")
	if err != nil {
		return "", err
	}

	handoff, err := a.repo.LoadRequired("handoff.md")
	if err != nil {
		return "", err
	}

	var b strings.Builder
	appendSection(&b, base)
	appendSection(&b, calm)
	appendSection(&b, applyVariables(modeBody, ctx))
	appendSection(&b, approval)
	appendSection(&b, dynamicSection("Project Context", ctx.MemoryContext, ctx.ExternalContext))
	appendSection(&b, dynamicSection("Skills", ctx.SkillIndex))
	appendSection(&b, ctxMgmt)
	appendSection(&b, handoff)

	assembled := strings.TrimSpace(b.String())
	if err := validateLanguage(assembled, "assembled prompt"); err != nil {
		return "", err
	}
	return assembled, nil
}

// approvalMode 归一审批模式:auto / never 透传,其余(含空)回落 suggest。
func approvalMode(ctx Context) string {
	normalized := strings.ToLower(strings.TrimSpace(ctx.ApprovalMode))
	switch normalized {
	case "auto", "never":
		return normalized
	default:
		return "suggest"
	}
}

// applyVariables 按行处理模式片段:某行含 {{key}} 且该变量为空(缺省或空串)时,
// 整行删除;否则把行内所有占位符替换成对应变量值。删行后残留的连续空行折叠成一个。
// 这样未提供 taskType/taskDescription 时,不会留下「任务类型:」这种空尾巴。
func applyVariables(template string, ctx Context) string {
	lines := strings.Split(template, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		drop := false
		for _, m := range placeholderRe.FindAllStringSubmatch(line, -1) {
			if strings.TrimSpace(ctx.Variable(m[1])) == "" {
				drop = true
				break
			}
		}
		if drop {
			continue
		}
		line = placeholderRe.ReplaceAllStringFunc(line, func(s string) string {
			return ctx.Variable(placeholderRe.FindStringSubmatch(s)[1])
		})
		out = append(out, line)
	}
	return blankLinesRe.ReplaceAllString(strings.Join(out, "\n"), "\n\n")
}

// dynamicSection 把若干非空值用空行拼接,加上「## Title」标题;全空则返回空串。
func dynamicSection(title string, values ...string) string {
	var body strings.Builder
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		if body.Len() > 0 {
			body.WriteString("\n\n")
		}
		body.WriteString(strings.TrimSpace(value))
	}
	if body.Len() == 0 {
		return ""
	}
	return "## " + title + "\n\n" + body.String()
}

// appendSection 把非空片段追加进 builder,段间用空行分隔。
func appendSection(b *strings.Builder, section string) {
	if strings.TrimSpace(section) == "" {
		return
	}
	if b.Len() > 0 {
		b.WriteString("\n\n")
	}
	b.WriteString(strings.TrimSpace(section))
}

// validateLanguage 校验文本含「## Language」段,缺则报错(对齐 Java 的语言段强校验)。
func validateLanguage(text, source string) error {
	if !strings.Contains(text, "## Language") {
		return fmt.Errorf("prompt %s must contain a '## Language' section", source)
	}
	return nil
}
