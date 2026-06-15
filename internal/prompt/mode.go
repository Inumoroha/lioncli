// Package prompt 把发给 LLM 的 system prompt 拆成模块化 Markdown 片段,
// 按固定顺序装配,并支持三层覆盖(内置 < 用户目录 < 项目目录)、{{变量}}
// 替换、多模式与动态段(Project Context / Skills)。
//
// 这是根目录 Java 版 com.paicli.prompt 的 Go 移植:classpath 资源改为
// //go:embed,unchecked 异常改为返回 error。本包是纯领域层,不依赖
// agent/llm/tool;把装配结果接到 system prompt 的桥接留待调用方。
package prompt

import "fmt"

// Mode 是一种提示词模式,每个模式对应一个 modes/<x>.md 片段。
type Mode int

const (
	ModeAgent Mode = iota
	ModePlan
	ModePlanner
	ModeTeamPlanner
	ModeTeamWorker
	ModeTeamReviewer
)

// resourcePath 返回该模式片段在仓库里的相对路径。
func (m Mode) resourcePath() string {
	switch m {
	case ModeAgent:
		return "modes/agent.md"
	case ModePlan:
		return "modes/plan.md"
	case ModePlanner:
		return "modes/planner.md"
	case ModeTeamPlanner:
		return "modes/team-planner.md"
	case ModeTeamWorker:
		return "modes/team-worker.md"
	case ModeTeamReviewer:
		return "modes/team-reviewer.md"
	default:
		return ""
	}
}

// String 让 Mode 可读,用于日志/错误。
func (m Mode) String() string {
	if p := m.resourcePath(); p != "" {
		return p
	}
	return fmt.Sprintf("Mode(%d)", int(m))
}
