package skill

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

const (
	MaxDescriptionCodepoints = 500
	MaxEnabledSkills         = 20
	MaxIndexBytes            = 4096
)

// FormatSkillIndex 生成进系统提示的"可用 Skills"索引段:每个技能一行 name+描述,
// 告诉 LLM 何时调用 load_skill 加载完整指引。超出上限会截断并 warn。
func FormatSkillIndex(enabled []*Skill) string {
	if len(enabled) == 0 {
		return ""
	}

	effective := enabled
	if len(enabled) > MaxEnabledSkills {
		effective = append([]*Skill(nil), enabled...)
		sort.Slice(effective, func(i, j int) bool {
			return effective[i].Name < effective[j].Name
		})
		effective = effective[:MaxEnabledSkills]
		fmt.Fprintf(os.Stderr, "⚠ 检测到 %d 个 skill，仅前 %d 个进入 system prompt 索引\n", len(enabled), MaxEnabledSkills)
	}

	var sb strings.Builder
	sb.WriteString("## 可用 Skills（按需调用 load_skill 加载完整指引）\n\n")

	for _, skill := range effective {
		if skill == nil {
			continue
		}
		desc := truncateByCodepoint(strings.TrimSpace(skill.Description), MaxDescriptionCodepoints)
		sb.WriteString("- **")
		sb.WriteString(skill.Name)
		sb.WriteString("**：")
		sb.WriteString(desc)
		sb.WriteByte('\n')
	}

	sb.WriteString("\n")
	sb.WriteString("判断准则：当任务描述匹配某个 skill 的触发场景时，调用 load_skill(name) 加载完整指引；")
	sb.WriteString("已加载的 skill 会在下一轮以 \"## 已加载 Skill\" 段落出现在你的 user message 中。")
	sb.WriteString("不要重复加载同一 skill；同一会话内一次足够。\n")

	result := sb.String()
	if len(result) > MaxIndexBytes {
		fmt.Fprintf(os.Stderr, "⚠ skill 索引段超过 %d 字节，已截断\n", MaxIndexBytes)
		return result[:MaxIndexBytes] + "\n...(skill 索引段被截断)\n"
	}

	return result
}

func truncateByCodepoint(s string, limit int) string {
	if s == "" {
		return ""
	}

	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "..."
}
