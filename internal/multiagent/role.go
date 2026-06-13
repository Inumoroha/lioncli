package multiagent

// AgentRole 是多智能体团队里的角色。底层是 string,便于直接进消息/日志。
type AgentRole string

const (
	AgentRolePlanner  AgentRole = "PLANNER"
	AgentRoleWorker   AgentRole = "WORKER"
	AgentRoleReviewer AgentRole = "REVIEWER"
)

var roleNames = map[AgentRole]string{
	AgentRolePlanner:  "规划者",
	AgentRoleWorker:   "执行者",
	AgentRoleReviewer: "检查者",
}

var roleDescriptions = map[AgentRole]string{
	AgentRolePlanner:  "负责分析用户任务，制定执行计划，将复杂任务拆解为可执行的子任务",
	AgentRoleWorker:   "负责执行具体任务步骤，调用工具完成文件操作、命令执行等操作",
	AgentRoleReviewer: "负责检查执行结果的质量和正确性，提供改进建议",
}

// roleSystemPrompts 是每个角色的基础人格设定(system prompt)。
// 任务相关的上下文由 orchestrator 在调用时拼进 user 消息,这里只定身份与产出格式。
var roleSystemPrompts = map[AgentRole]string{
	AgentRolePlanner: `你是多智能体协作团队中的「规划者」。
你的职责:把用户的总目标拆解为有序、可执行的子任务。
要求:
- 只输出一个 JSON 对象,不要任何解释或 markdown 代码围栏。
- 结构: {"steps":[{"id":1,"description":"...","dependencies":[]}]}
- id 为从 1 开始的连续整数;dependencies 列出本步骤依赖的前置步骤 id(没有则空数组)。
- 步骤数量控制在合理范围(通常 2~6 步),每步描述清晰、可独立交给执行者完成。
- 若任务本身很简单,可以只给 1 步。`,

	AgentRoleWorker: `你是多智能体协作团队中的「执行者」。
你的职责:完成分配给你的单个子任务。必要时调用可用工具(读写文件、执行命令、检索等)来真正落地,而不是仅给出文字方案。
要求:
- 聚焦当前子任务,不要越权去做计划里的其他步骤。
- 工具调用要谨慎、最小化副作用;危险操作可能需要用户批准。
- 完成后用简洁的自然语言总结你做了什么、产出/结论是什么。`,

	AgentRoleReviewer: `你是多智能体协作团队中的「检查者」。
你的职责:审查执行者针对某子任务交付的结果,判断其是否正确、完整、达标。
输出格式(严格遵守,只输出一行 JSON):
{"verdict":"APPROVED"} 或 {"verdict":"REJECTED","feedback":"需要改进的具体说明"}
- 只有确实存在问题时才 REJECTED,并给出可操作的改进建议;否则一律 APPROVED。
- 不要重做任务本身,只做评判。`,
}

func (r AgentRole) Name() string { return roleNames[r] }

func (r AgentRole) Description() string { return roleDescriptions[r] }

// SystemPrompt 返回该角色的基础 system prompt。
func (r AgentRole) SystemPrompt() string { return roleSystemPrompts[r] }

// IsValid 防止外部传入非法角色字符串。
func (r AgentRole) IsValid() bool {
	_, ok := roleNames[r]
	return ok
}
