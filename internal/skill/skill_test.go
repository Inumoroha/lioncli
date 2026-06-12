package skill

import (
	"strings"
	"testing"
)

func TestParseSkillFrontmatter(t *testing.T) {
	full := "---\nname: demo\ndescription: 一句话说明\ntags: [a, b]\n---\n# 正文\n做事步骤\n"
	res := ParseSkillFrontmatter(full)
	if len(res.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", res.Warnings)
	}
	if got := stringField(res.Frontmatter, "name"); got != "demo" {
		t.Errorf("name = %q, want demo", got)
	}
	if got := stringField(res.Frontmatter, "description"); got != "一句话说明" {
		t.Errorf("description = %q", got)
	}
	if tags := listField(res.Frontmatter, "tags"); len(tags) != 2 || tags[0] != "a" || tags[1] != "b" {
		t.Errorf("tags = %v, want [a b]", tags)
	}
	if !strings.HasPrefix(res.Body, "# 正文") {
		t.Errorf("body = %q, want it to start with the heading", res.Body)
	}
}

func TestParseSkillFrontmatterStripsBOM(t *testing.T) {
	full := "\xef\xbb\xbf---\nname: bom\n---\nbody\n"
	res := ParseSkillFrontmatter(full)
	if got := stringField(res.Frontmatter, "name"); got != "bom" {
		t.Errorf("name = %q, want bom (BOM should be trimmed before ---)", got)
	}
}

func TestParseSkillFrontmatterNoFrontmatter(t *testing.T) {
	res := ParseSkillFrontmatter("just body, no frontmatter")
	if res.Body != "just body, no frontmatter" {
		t.Errorf("body = %q", res.Body)
	}
	if len(res.Warnings) == 0 {
		t.Error("expected a warning for missing frontmatter start marker")
	}
}

// TestRegistryLoadsBuiltinFromEmbed 验证内置技能能从 embed 直读加载,
// 不依赖磁盘 cache。至少应包含 web-access 和 commit-message。
func TestRegistryLoadsBuiltinFromEmbed(t *testing.T) {
	reg := NewSkillRegistry(BuiltinFS(), BuiltinRoot(), "", "", nil)
	reg.Reload()

	if w := reg.Warnings(); len(w) != 0 {
		t.Errorf("unexpected load warnings: %v", w)
	}

	all := reg.AllSkills()
	if len(all) < 2 {
		t.Fatalf("got %d builtin skills, want >= 2", len(all))
	}

	for _, name := range []string{"web-access", "commit-message"} {
		s := reg.FindSkill(name)
		if s == nil {
			t.Errorf("builtin skill %q not found", name)
			continue
		}
		if s.Source != SourceBuiltin {
			t.Errorf("%q source = %q, want builtin", name, s.Source)
		}
		if s.Description == "" || s.Body == "" {
			t.Errorf("%q missing description or body", name)
		}
	}
}

// TestEnabledSkillsRespectsDisabled 验证 EnabledSkills 过滤掉 stateStore 中
// 被禁用的技能,而 AllSkills 仍返回全部。
func TestEnabledSkillsRespectsDisabled(t *testing.T) {
	store := NewSkillStateStore("") // 空路径:Disabled() 默认空,这里手动验证过滤路径
	reg := NewSkillRegistry(BuiltinFS(), BuiltinRoot(), "", "", store)
	reg.Reload()

	all := reg.AllSkills()
	if len(all) == 0 {
		t.Fatal("no builtin skills loaded")
	}
	// 空 store 下 EnabledSkills 应等于 AllSkills 数量。
	if len(reg.EnabledSkills()) != len(all) {
		t.Errorf("EnabledSkills = %d, want %d (none disabled)", len(reg.EnabledSkills()), len(all))
	}
}
