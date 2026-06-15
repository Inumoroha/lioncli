package prompt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAssembleAgentFull(t *testing.T) {
	a := NewAssembler(NewRepository(builtinFS, "", ""))
	ctx := Context{
		MemoryContext:   "记忆:用户偏好简体中文。",
		ExternalContext: "外部:当前在 teacli 仓库。",
		SkillIndex:      "## 可用 Skills（按需调用 load_skill 加载完整指引）\n\n- **web-access**：联网检索指引。",
		Variables: map[string]string{
			"taskType":        "重构",
			"taskDescription": "移植 prompt 包",
		},
	}

	out, err := a.Assemble(ModeAgent, ctx)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	// 必含语言段(校验依赖)。
	if !strings.Contains(out, "## Language") {
		t.Error("缺少 ## Language 段")
	}
	// 各固定片段标志串按序出现。
	for _, want := range []string{
		"你是 teacli",          // base
		"## 风格",              // personalities/calm
		"## 模式:Agent",        // mode agent
		"## 审批策略:Suggest",    // approvals/suggest(默认)
		"## Project Context", // 动态段
		"记忆:用户偏好简体中文。",
		"## Skills",
		"web-access",
		"## 上下文管理", // context-management
		"## 收尾",    // handoff
	} {
		if !strings.Contains(out, want) {
			t.Errorf("装配结果缺少片段标志: %q", want)
		}
	}

	// 变量替换生效,且没有遗留占位符。
	if !strings.Contains(out, "任务类型:重构") || !strings.Contains(out, "任务描述:移植 prompt 包") {
		t.Error("变量未正确替换")
	}
	if strings.Contains(out, "{{taskType}}") || strings.Contains(out, "{{taskDescription}}") {
		t.Error("仍有未替换的占位符")
	}
}

func TestApprovalModeNormalization(t *testing.T) {
	a := NewAssembler(NewRepository(builtinFS, "", ""))

	cases := map[string]string{
		"auto":    "## 审批策略:Auto",
		"never":   "## 审批策略:只读",
		"":        "## 审批策略:Suggest",
		"garbage": "## 审批策略:Suggest",
	}
	for mode, want := range cases {
		out, err := a.Assemble(ModeAgent, Context{ApprovalMode: mode})
		if err != nil {
			t.Fatalf("Assemble(%q): %v", mode, err)
		}
		if !strings.Contains(out, want) {
			t.Errorf("approvalMode=%q 应产出 %q", mode, want)
		}
	}
}

func TestSkillsSectionOmittedWhenEmpty(t *testing.T) {
	a := NewAssembler(NewRepository(builtinFS, "", ""))
	out, err := a.Assemble(ModeAgent, Context{}) // 无 skillIndex / 无 memory
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if strings.Contains(out, "## Skills") {
		t.Error("skillIndex 为空时不应出现 ## Skills 段")
	}
	if strings.Contains(out, "## Project Context") {
		t.Error("memory/external 为空时不应出现 ## Project Context 段")
	}
}

func TestProjectOverridesUser(t *testing.T) {
	userDir := t.TempDir()
	projectDir := t.TempDir()

	// 用户层与项目层都覆盖 base.md,项目层应胜出;两者都带 ## Language 以过校验。
	writeFile(t, filepath.Join(userDir, "base.md"), "用户层 base\n## Language\nuser")
	writeFile(t, filepath.Join(projectDir, "base.md"), "项目层 base\n## Language\nproject")

	a := NewAssembler(NewRepository(builtinFS, userDir, projectDir))
	out, err := a.Assemble(ModeAgent, Context{})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if !strings.Contains(out, "项目层 base") {
		t.Error("项目层覆盖未生效")
	}
	if strings.Contains(out, "用户层 base") || strings.Contains(out, "你是 teacli") {
		t.Error("项目层应覆盖用户层与内置")
	}
}

func TestUserOverridesBuiltin(t *testing.T) {
	userDir := t.TempDir()
	writeFile(t, filepath.Join(userDir, "personalities", "calm.md"), "## 自定义人格\n更活泼")

	a := NewAssembler(NewRepository(builtinFS, userDir, ""))
	out, err := a.Assemble(ModeAgent, Context{})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if !strings.Contains(out, "## 自定义人格") {
		t.Error("用户层覆盖内置未生效")
	}
}

func TestLoadRequiredRejectsTraversal(t *testing.T) {
	r := NewRepository(builtinFS, "", "")
	for _, bad := range []string{"../secret", "/etc/passwd", "a/../../b", ""} {
		if _, err := r.LoadRequired(bad); err == nil {
			t.Errorf("LoadRequired(%q) 应返回 error", bad)
		}
	}
}

func TestAllDeclaredModesAssemble(t *testing.T) {
	a := NewAssembler(NewRepository(builtinFS, "", ""))

	for _, mode := range []Mode{
		ModeAgent,
		ModePlan,
		ModePlanner,
		ModeTeamPlanner,
		ModeTeamWorker,
		ModeTeamReviewer,
	} {
		out, err := a.Assemble(mode, Context{
			Variables: map[string]string{
				"taskType":        "测试",
				"taskDescription": "确认所有声明的 prompt mode 都有内置片段",
			},
		})
		if err != nil {
			t.Fatalf("Assemble(%s): %v", mode, err)
		}
		if !strings.Contains(out, "## Language") {
			t.Fatalf("Assemble(%s) missing language section", mode)
		}
	}
}

func TestUnknownModeErrors(t *testing.T) {
	a := NewAssembler(NewRepository(builtinFS, "", ""))
	if _, err := a.Assemble(Mode(999), Context{}); err == nil {
		t.Error("未知模式应返回 error")
	}
}

func TestAgentModeHidesEmptyTaskLines(t *testing.T) {
	a := NewAssembler(NewRepository(builtinFS, "", ""))

	// 不提供 taskType/taskDescription:对应行应整行消失,不留空尾巴。
	out, err := a.Assemble(ModeAgent, Context{})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if strings.Contains(out, "任务类型") || strings.Contains(out, "任务描述") {
		t.Error("空变量时「任务类型/描述」行应被隐藏")
	}
	if strings.Contains(out, "{{") {
		t.Error("不应残留未替换占位符")
	}
	// 删行后不应留下三连以上换行。
	if strings.Contains(out, "\n\n\n") {
		t.Error("删行后应折叠连续空行")
	}

	// 提供变量时该行保留并替换。
	out2, err := a.Assemble(ModeAgent, Context{Variables: map[string]string{"taskType": "重构"}})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if !strings.Contains(out2, "任务类型:重构") {
		t.Error("提供 taskType 时该行应保留并替换")
	}
	// taskDescription 仍为空,该行应被隐藏。
	if strings.Contains(out2, "任务描述") {
		t.Error("taskDescription 为空时该行应隐藏")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
