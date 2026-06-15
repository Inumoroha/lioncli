package agent

// EventKind 标识 agent 在一轮对话中发出的事件类型。
// TUI 之类的旁观者订阅 chan<- Event 后能实时知道 LLM 文字输出、工具调用、错误。
type EventKind string

const (
	EventAssistantText EventKind = "assistant_text"
	EventToolStart     EventKind = "tool_start"
	EventToolEnd       EventKind = "tool_end"
	EventError         EventKind = "error"
	EventInfo          EventKind = "info" // 一般性进度旁白(如多智能体协作的角色/审查动态)

	// 规划模式事件(Plan 方法)。
	EventPlanStart EventKind = "plan_start" // 开始生成计划(Text=goal)
	EventPlanReady EventKind = "plan_ready" // 计划已生成(Text=plan 可视化)
	EventTaskStart EventKind = "task_start" // 开始执行一个任务(Text=任务序号+描述)
	EventPlanDone  EventKind = "plan_done"  // 全部任务完成(Text=结果摘要)
)

// Event 是一个联合体。不同 Kind 用不同字段：
//   - AssistantText：Text
//   - ToolStart：ToolID, ToolName, ToolInput
//   - ToolEnd：ToolID, ToolName, ToolOutput, IsError
//   - Error：Err
//   - PlanStart/PlanReady/PlanDone：Text
//   - TaskStart：Text(序号+描述)
type Event struct {
	Kind       EventKind
	Text       string         // AssistantText
	ToolID     string         // ToolStart / ToolEnd 用于关联同一次调用
	ToolName   string         // ToolStart / ToolEnd
	ToolInput  map[string]any // ToolStart
	ToolOutput string         // ToolEnd
	IsError    bool           // ToolEnd
	Err        error          // Error
}

// SetEvents 把事件出口接到一个通道。传 nil 表示停止订阅。
// 调用方负责创建/关闭通道，agent 本身只发不收。
func (a *Agent) SetEvents(ch chan<- Event) {
	a.events = ch
}

// emit 把一个事件投递到订阅者。
// 没有订阅者时静默丢弃；有订阅者时阻塞投递，保证事件顺序与执行顺序一致。
// agent.Run 跑在调用方的 goroutine 里（典型场景是 tea.Cmd），TUI 在主循环里消费，
// 双方分别在两个 goroutine，互不阻塞。
func (a *Agent) emit(e Event) {
	if a.events == nil {
		return
	}
	a.events <- e
}
