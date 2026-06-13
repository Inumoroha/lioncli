package multiagent

// EventKind 标识编排过程中发出的事件类型,供宿主(经 agent.RunTeam)实时渲染协作过程。
type EventKind string

const (
	EventPlanStart  EventKind = "plan_start"  // 规划者开始拆解(Text=goal)
	EventPlanReady  EventKind = "plan_ready"  // 计划生成完毕(Text=步骤清单可视化)
	EventStepStart  EventKind = "step_start"  // 某子任务开始(Text=序号+描述)
	EventStepResult EventKind = "step_result" // 某子任务执行者产出(Text=结果摘要)
	EventReview     EventKind = "review"      // 检查者结论(Text=通过/驳回+反馈)
	EventToolStart  EventKind = "tool_start"  // 执行者发起工具调用
	EventToolEnd    EventKind = "tool_end"    // 工具调用结束
	EventInfo       EventKind = "info"        // 一般性进度旁白
	EventDone       EventKind = "done"        // 全流程结束(Text=最终汇总)
	EventError      EventKind = "error"       // 出错(Text=错误信息)
)

// Event 是编排器对外的进度事件。不同 Kind 用到不同字段:
//   - 工具事件额外带 ToolID/ToolName/ToolInput/ToolOutput/IsError,便于宿主复用工具块渲染。
type Event struct {
	Kind      EventKind
	Agent     string    // 发出事件的子代理名(如 worker-1)
	Role      AgentRole // 发出事件的角色
	Text      string
	ToolID    string
	ToolName  string
	ToolInput map[string]any
	IsError   bool
}

// Observer 接收编排进度事件。为 nil 时编排器静默运行。
type Observer func(Event)
