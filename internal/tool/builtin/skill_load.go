package builtin

import (
	"context"
	"fmt"

	"lioncli/internal/skill"
	"lioncli/internal/tool"
)

// NewLoadSkillTool 构造统一的 load_skill 工具,替代旧的每技能一个 skill__ 工具。
//
// 设计上沿用"渐进式披露"(progressive disclosure):
//   - 技能的 name/description 由 skill.FormatSkillIndex 注入 system prompt,
//     始终对 LLM 可见,占用上下文很小,让它知道有哪些技能、何时该用。
//   - 技能正文(怎么做)只在 LLM 调用 load_skill 后才进入上下文;且正文**不**
//     塞进工具结果,而是 Push 进共享的 SkillContextBuffer,由 agent 在下一轮
//     Chat 前 Drain 出来、作为一条 user message 注入(见 internal/agent)。
//
// reg 与 buf 由 main.go 装配,buf 同一实例同时传给本工具和 agent,共享指针。
func NewLoadSkillTool(reg *skill.SkillRegistry, buf *skill.SkillContextBuffer) tool.Tool {
	return tool.Tool{
		Name: "load_skill",
		Description: "Load the full step-by-step instructions for a skill listed in the " +
			"\"可用 Skills\" section of your system prompt. Call this with the skill's name " +
			"before performing a task that matches the skill. The instructions will appear " +
			"in your next user message under a \"## 已加载 Skill\" heading.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "The skill name to load, exactly as shown in the skills index.",
				},
			},
			"required": []string{"name"},
		},
		Execute: func(_ context.Context, args map[string]any) (string, error) {
			name := tool.StringArg(args, "name")
			if name == "" {
				return "", fmt.Errorf("missing required parameter: name")
			}

			s := reg.FindSkill(name)
			if s == nil {
				// 找不到/被禁用:返回可读提示而非 error,不中断对话循环,
				// 让 LLM 据此换名重试或改走别的路径。
				return fmt.Sprintf("未找到名为 %q 的可用 skill,请对照系统提示里的技能索引确认名称。", name), nil
			}

			buf.Push(s.Name, s.Body)
			return fmt.Sprintf("已加载 skill %q,其完整指引将在下一条消息中提供。", s.Name), nil
		},
	}
}
