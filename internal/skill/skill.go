// Package skill 把"技能"建模成一段可按需加载的操作指引。
//
// 一个 Skill 就是一个目录,里面有 SKILL.md:开头用 --- 包起来的 frontmatter
// 声明 name/description 等元数据,正文是教 agent 怎么完成某类任务的指引。
//
// 技能有三个来源(Source):内置(随二进制 embed)、用户目录、项目目录。
// 本包是领域层:负责解析 SKILL.md、维护启用/禁用状态、生成系统提示索引、
// 缓冲已加载技能正文。把技能暴露给 LLM 的桥接(load_skill 工具)在 tool 包,
// 保持本包单向被依赖、无环。
package skill

import "fmt"

// Source 标记一个技能的来源。
type Source string

const (
	SourceBuiltin Source = "builtin"
	SourceUser    Source = "user"
	SourceProject Source = "project"
)

// Skill 是一段被解析出来的技能指引及其元数据。
type Skill struct {
	Name          string
	Description   string
	Version       string
	Author        string
	Tags          []string
	Source        Source
	Body          string
	SkillMDPath   string
	ReferencesDir string
}

// NewSkill 构建一个 Skill,name 为空视为错误。其余字段做空值规整。
func NewSkill(
	name string,
	description string,
	version string,
	author string,
	tags []string,
	source Source,
	body string,
	skillMDPath string,
	referencesDir string,
) (*Skill, error) {
	if name == "" {
		return nil, fmt.Errorf("skill name 不能为空")
	}

	skill := &Skill{
		Name:          name,
		Description:   description,
		Version:       version,
		Author:        author,
		Tags:          append([]string(nil), tags...),
		Source:        source,
		Body:          body,
		SkillMDPath:   skillMDPath,
		ReferencesDir: referencesDir,
	}

	if skill.Tags == nil {
		skill.Tags = []string{}
	}

	return skill, nil
}

// DisplaySource 返回来源的可读字符串。
func (s *Skill) DisplaySource() string {
	if s == nil {
		return ""
	}
	return string(s.Source)
}
