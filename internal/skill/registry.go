package skill

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"sync"
)

// SkillRegistry 汇总三个来源的技能:内置(embed.FS)、用户目录、项目目录。
// 内置走 embed 直读、不落盘;user/project 来源扫真实文件系统目录。
// 通过 stateStore 过滤被禁用的技能。
type SkillRegistry struct {
	builtinFS        fs.FS
	builtinRoot      string
	userSkillsDir    string
	projectSkillsDir string
	stateStore       *SkillStateStore

	mu           sync.Mutex
	skillsByName map[string]*Skill
	warnings     []string
}

// NewSkillRegistry 创建注册表。builtinFS+builtinRoot 指向 embed 的内置技能根;
// userSkillsDir/projectSkillsDir 为空表示该来源不启用。
func NewSkillRegistry(
	builtinFS fs.FS,
	builtinRoot string,
	userSkillsDir string,
	projectSkillsDir string,
	stateStore *SkillStateStore,
) *SkillRegistry {
	return &SkillRegistry{
		builtinFS:        builtinFS,
		builtinRoot:      builtinRoot,
		userSkillsDir:    userSkillsDir,
		projectSkillsDir: projectSkillsDir,
		stateStore:       stateStore,
		skillsByName:     make(map[string]*Skill),
		warnings:         make([]string, 0),
	}
}

// Reload 重新加载全部来源。后加载的来源同名会覆盖先加载的:
// 即 project > user > builtin 的优先级。
func (r *SkillRegistry) Reload() {
	if r == nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.skillsByName = make(map[string]*Skill)
	r.warnings = r.warnings[:0]

	r.loadEmbedded(r.builtinFS, r.builtinRoot, SourceBuiltin)
	r.loadDirectory(r.userSkillsDir, SourceUser)
	r.loadDirectory(r.projectSkillsDir, SourceProject)
}

func (r *SkillRegistry) AllSkills() []*Skill {
	if r == nil {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	skills := make([]*Skill, 0, len(r.skillsByName))
	for _, skill := range r.skillsByName {
		skills = append(skills, skill)
	}
	sort.Slice(skills, func(i, j int) bool {
		return skills[i].Name < skills[j].Name
	})
	return skills
}

func (r *SkillRegistry) EnabledSkills() []*Skill {
	if r == nil {
		return nil
	}

	disabled := map[string]struct{}{}
	if r.stateStore != nil {
		disabled = r.stateStore.Disabled()
	}

	all := r.AllSkills()
	enabled := make([]*Skill, 0, len(all))
	for _, skill := range all {
		if _, found := disabled[skill.Name]; !found {
			enabled = append(enabled, skill)
		}
	}
	return enabled
}

func (r *SkillRegistry) FindSkill(name string) *Skill {
	if r == nil || name == "" {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	skill := r.skillsByName[name]
	if skill == nil {
		return nil
	}

	if r.stateStore != nil {
		if _, found := r.stateStore.Disabled()[name]; found {
			return nil
		}
	}
	return skill
}

func (r *SkillRegistry) FindAnySkill(name string) *Skill {
	if r == nil || name == "" {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	return r.skillsByName[name]
}

func (r *SkillRegistry) Warnings() []string {
	if r == nil {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.warnings...)
}

func (r *SkillRegistry) StateStore() *SkillStateStore {
	if r == nil {
		return nil
	}
	return r.stateStore
}

// loadEmbedded 从 embed.FS(或任意 fs.FS)的 root 目录加载内置技能:
// root 下每个含 SKILL.md 的子目录解析成一个 Skill。内置只有 SKILL.md,
// 不处理 references 目录。调用方已持锁。
func (r *SkillRegistry) loadEmbedded(fsys fs.FS, root string, source Source) {
	if fsys == nil || root == "" {
		return
	}

	entries, err := fs.ReadDir(fsys, root)
	if err != nil {
		msg := fmt.Sprintf("读取内置 skill 根失败 %s: %v", root, err)
		r.warnings = append(r.warnings, msg)
		fmt.Fprintf(os.Stderr, "⚠ %s\n", msg)
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		slug := entry.Name()
		mdPath := path.Join(root, slug, "SKILL.md")
		content, err := fs.ReadFile(fsys, mdPath)
		if err != nil {
			// 子目录没有 SKILL.md:不是技能目录,静默跳过。
			continue
		}
		skill := r.buildSkill(slug, string(content), mdPath, "", source)
		if skill != nil {
			r.skillsByName[skill.Name] = skill
		}
	}
}

// loadDirectory 扫描真实文件系统目录(user/project 来源)。调用方已持锁。
func (r *SkillRegistry) loadDirectory(dir string, source Source) {
	if dir == "" {
		return
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		msg := fmt.Sprintf("扫描 skill 目录失败 %s: %v", dir, err)
		r.warnings = append(r.warnings, msg)
		fmt.Fprintf(os.Stderr, "⚠ %s\n", msg)
		return
	}

	dirs := make([]string, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			dirs = append(dirs, filepath.Join(dir, entry.Name()))
		}
	}
	sort.Strings(dirs)

	for _, entryDir := range dirs {
		skillMD := filepath.Join(entryDir, "SKILL.md")
		info, err := os.Stat(skillMD)
		if err != nil || info.IsDir() {
			continue
		}
		content, err := os.ReadFile(skillMD)
		if err != nil {
			msg := fmt.Sprintf("读取 SKILL.md 失败 %s: %v", skillMD, err)
			r.warnings = append(r.warnings, msg)
			fmt.Fprintf(os.Stderr, "⚠ %s\n", msg)
			continue
		}

		referencesDir := filepath.Join(entryDir, "references")
		if info, err := os.Stat(referencesDir); err != nil || !info.IsDir() {
			referencesDir = ""
		}

		skill := r.buildSkill(filepath.Base(entryDir), string(content), skillMD, referencesDir, source)
		if skill != nil {
			r.skillsByName[skill.Name] = skill
		}
	}
}

// buildSkill 把 SKILL.md 文本解析成 *Skill,name 缺省回退为 slug。
// 解析告警与构建失败都记进 r.warnings,坏掉一个不阻断其余。调用方已持锁。
func (r *SkillRegistry) buildSkill(slug, content, skillMDPath, referencesDir string, source Source) *Skill {
	parsed := ParseSkillFrontmatter(content)
	for _, warning := range parsed.Warnings {
		msg := fmt.Sprintf("%s: %s", skillMDPath, warning)
		r.warnings = append(r.warnings, msg)
		fmt.Fprintf(os.Stderr, "⚠ Skill %s frontmatter: %s\n", skillMDPath, warning)
	}

	name := stringField(parsed.Frontmatter, "name")
	if name == "" {
		name = slug
	}
	description := stringField(parsed.Frontmatter, "description")
	version := stringField(parsed.Frontmatter, "version")
	author := stringField(parsed.Frontmatter, "author")
	tags := listField(parsed.Frontmatter, "tags")

	skill, err := NewSkill(
		name,
		description,
		version,
		author,
		tags,
		source,
		parsed.Body,
		skillMDPath,
		referencesDir,
	)
	if err != nil {
		msg := fmt.Sprintf("构建 Skill 失败 %s: %v", skillMDPath, err)
		r.warnings = append(r.warnings, msg)
		fmt.Fprintf(os.Stderr, "⚠ %s\n", msg)
		return nil
	}

	return skill
}

func stringField(frontmatter map[string]any, key string) string {
	value, ok := frontmatter[key]
	if !ok {
		return ""
	}
	s, ok := value.(string)
	if !ok {
		return ""
	}
	return s
}

func listField(frontmatter map[string]any, key string) []string {
	value, ok := frontmatter[key]
	if !ok {
		return []string{}
	}

	list, ok := value.([]string)
	if ok {
		return append([]string(nil), list...)
	}

	generic, ok := value.([]any)
	if !ok {
		return []string{}
	}

	result := make([]string, 0, len(generic))
	for _, item := range generic {
		if s, ok := item.(string); ok {
			result = append(result, s)
		}
	}
	return result
}
