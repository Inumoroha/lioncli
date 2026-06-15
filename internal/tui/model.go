package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"lioncli/internal/browser"
	"lioncli/internal/hitl"
	"lioncli/internal/mcp"
	runtimetask "lioncli/internal/runtime/task"
)

type (
	eventMsg     Event
	agentDoneMsg struct {
		text string
		err  error
	}
	planDoneMsg struct {
		text string
		err  error
	}
	indexDoneMsg struct {
		text string
		err  error
	}
)

const (
	inputHeight         = 3
	maxCommandMenuItems = 6
)

type model struct {
	agent      Agent
	events     chan Event
	viewport   viewport.Model
	input      textarea.Model
	spinner    spinner.Model
	md         *glamour.TermRenderer
	history    []renderedBlock
	waiting    bool
	width      int
	height     int
	modelName  string
	statusLine string

	bridge      *approvalBridge
	hitlHandler hitl.HitlHandler
	pending     *hitl.ApprovalRequest

	taskManager      *runtimetask.Manager
	taskInitErr      string
	mcpStatus        func() mcp.StatusSnapshot
	commandMenuIndex int
}

func newModel(a Agent, modelName string, mcpStatus func() mcp.StatusSnapshot) model {
	ta := textarea.New()
	ta.Placeholder = "Ask anything..."
	ta.Prompt = "> "
	ta.CharLimit = 0
	ta.SetHeight(inputHeight)
	ta.ShowLineNumbers = false
	ta.Focus()

	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	sp.Style = lipgloss.NewStyle().Foreground(colorAccent)

	vp := viewport.New(80, 20)

	bridge := newApprovalBridge()
	handler := hitl.NewRendererHitlHandler(bridge, true)
	if a != nil {
		a.SetHitl(handler)
		guard := browser.NewGuard(browser.NewSession(), browser.NewSensitivePagePolicy(sensitivePatternsPath()))
		a.SetBrowserGuard(guard)
	}

	taskManager, taskInitErr := newTaskManager(a)

	return model{
		agent:       a,
		viewport:    vp,
		input:       ta,
		spinner:     sp,
		modelName:   modelName,
		bridge:      bridge,
		hitlHandler: handler,
		taskManager: taskManager,
		taskInitErr: taskInitErr,
		mcpStatus:   mcpStatus,
	}
}

func newTaskManager(a Agent) (*runtimetask.Manager, string) {
	manager, err := runtimetask.NewManager("", func(ctx context.Context, prompt string) (string, error) {
		if a == nil {
			return "", fmt.Errorf("background agent execution is not configured")
		}
		return a.Run(ctx, prompt)
	}, 1)
	if err != nil {
		return nil, err.Error()
	}
	return manager, ""
}

func (m model) Init() tea.Cmd {
	if m.bridge == nil {
		return textarea.Blink
	}
	return tea.Batch(textarea.Blink, m.bridge.waitForApproval())
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resize()
		m.refreshViewport()
		return m, nil

	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC || msg.Type == tea.KeyCtrlD {
			return m, tea.Quit
		}
		if m.pending != nil {
			return m.handleApprovalKey(msg)
		}
		if !m.waiting {
			if updated, handled := m.handleCommandMenuKey(msg); handled {
				return updated, nil
			}
		}
		if msg.Type == tea.KeyEnter && !m.waiting && !msg.Alt {
			return m.submitInput()
		}

	case eventMsg:
		m.applyEvent(Event(msg))
		m.refreshViewport()
		return m, m.waitForEvent(m.events)

	case approvalMsg:
		req := hitl.ApprovalRequest(msg)
		m.pending = &req
		m.history = append(m.history, renderedBlock{kind: blockApproval, approval: &req})
		m.refreshViewport()
		return m, nil

	case agentDoneMsg:
		m.waiting = false
		m.statusLine = m.tokenUsage()
		if msg.err != nil {
			m.history = append(m.history, renderedBlock{kind: blockError, body: msg.err.Error()})
		}
		m.refreshViewport()
		m.input.Focus()
		return m, nil

	case planDoneMsg:
		m.waiting = false
		m.statusLine = m.tokenUsage()
		if msg.err != nil {
			m.history = append(m.history, renderedBlock{kind: blockError, body: fmt.Sprintf("plan failed: %v", msg.err)})
		} else if strings.TrimSpace(msg.text) != "" {
			m.history = append(m.history, renderedBlock{kind: blockSystem, body: msg.text})
		}
		m.refreshViewport()
		m.input.Focus()
		return m, nil

	case indexDoneMsg:
		m.waiting = false
		if msg.err != nil {
			m.history = append(m.history, renderedBlock{kind: blockError, body: fmt.Sprintf("index failed: %v", msg.err)})
		} else {
			m.history = append(m.history, renderedBlock{kind: blockSystem, body: msg.text})
		}
		m.refreshViewport()
		m.input.Focus()
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		if m.waiting {
			return m, cmd
		}
		return m, nil
	}

	var cmd tea.Cmd
	if !m.waiting {
		m.input, cmd = m.input.Update(msg)
		m.clampCommandMenuIndex()
		cmds = append(cmds, cmd)
	}
	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
}

func (m model) submitInput() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return m, nil
	}

	if strings.HasPrefix(text, "/team ") || text == "/team" {
		return m.startAgentLikeCommand(text, "/team", "Usage: /team <goal>", m.runTeam)
	}
	if strings.HasPrefix(text, "/plan ") || text == "/plan" {
		return m.startAgentLikeCommand(text, "/plan", "Usage: /plan <goal>", m.runPlan)
	}
	if strings.HasPrefix(text, "/index ") || text == "/index" {
		path := strings.TrimSpace(strings.TrimPrefix(text, "/index"))
		m.history = append(m.history, renderedBlock{kind: blockUser, body: text}, renderedBlock{kind: blockSystem, body: "Indexing project..."})
		m.input.Reset()
		m.waiting = true
		m.refreshViewport()
		return m, tea.Batch(m.runIndex(path), m.spinner.Tick)
	}
	if strings.HasPrefix(text, "/") {
		m.input.Reset()
		m.history = append(m.history, renderedBlock{kind: blockUser, body: text}, renderedBlock{kind: blockSystem, body: m.handleCommandV2(text)})
		m.statusLine = m.tokenUsage()
		m.refreshViewport()
		return m, nil
	}

	if m.agent == nil {
		m.input.Reset()
		m.history = append(m.history, renderedBlock{kind: blockUser, body: text}, renderedBlock{kind: blockError, body: "agent is not configured"})
		m.refreshViewport()
		return m, nil
	}

	m.history = append(m.history, renderedBlock{kind: blockUser, body: text})
	m.input.Reset()
	m.waiting = true
	m.events = make(chan Event, 32)
	m.agent.SetEvents(m.events)
	m.refreshViewport()
	return m, tea.Batch(m.runAgent(text, m.events), m.waitForEvent(m.events), m.spinner.Tick)
}

func (m model) startAgentLikeCommand(text, prefix, usage string, run func(string, chan Event) tea.Cmd) (tea.Model, tea.Cmd) {
	goal := strings.TrimSpace(strings.TrimPrefix(text, prefix))
	if goal == "" {
		m.input.Reset()
		m.history = append(m.history, renderedBlock{kind: blockUser, body: text}, renderedBlock{kind: blockSystem, body: usage})
		m.refreshViewport()
		return m, nil
	}
	if m.agent == nil {
		m.input.Reset()
		m.history = append(m.history, renderedBlock{kind: blockUser, body: text}, renderedBlock{kind: blockError, body: "agent is not configured"})
		m.refreshViewport()
		return m, nil
	}
	m.history = append(m.history, renderedBlock{kind: blockUser, body: text})
	m.input.Reset()
	m.waiting = true
	m.events = make(chan Event, 32)
	m.agent.SetEvents(m.events)
	m.refreshViewport()
	return m, tea.Batch(run(goal, m.events), m.waitForEvent(m.events), m.spinner.Tick)
}

func (m model) View() string {
	if m.width == 0 {
		return "initializing..."
	}
	title := fmt.Sprintf("lioncli · %s", m.modelName)
	if m.statusLine != "" {
		title += " · " + m.statusLine
	}
	header := headerStyle.Width(m.width - 2).Render(title)
	chat := viewportStyle.Render(m.viewport.View())

	var inputArea string
	switch {
	case m.pending != nil:
		inputArea = inputStyle.Render(approvalHintStyle.Render(approvalKeyHint(*m.pending)))
	case m.waiting:
		inputArea = inputStyle.Render(fmt.Sprintf("%s waiting for LLM...", m.spinner.View()))
	default:
		inputArea = inputStyle.Render(m.input.View())
		if menu := m.renderCommandMenu(); menu != "" {
			inputArea = lipgloss.JoinVertical(lipgloss.Left, menu, inputArea)
		}
	}

	help := helpStyle.Width(m.width - 2).Render(
		"Enter: send   Shift+Enter: newline   @image:<path>/@clipboard   /help /tools /task /plan /team /index /memory /hitl   Ctrl+C: quit",
	)
	return lipgloss.JoinVertical(lipgloss.Left, header, chat, inputArea, help)
}

func (m *model) resize() {
	if m.width == 0 || m.height == 0 {
		return
	}
	headerH := 3
	helpH := 3
	inputH := inputHeight + 2
	vpH := m.height - headerH - helpH - inputH - 2
	if vpH < 3 {
		vpH = 3
	}
	innerW := m.width - 4
	if innerW < 10 {
		innerW = 10
	}
	m.viewport.Width = innerW
	m.viewport.Height = vpH
	m.input.SetWidth(innerW)

	wrap := innerW - 2
	if wrap < 20 {
		wrap = 20
	}
	if md, err := glamour.NewTermRenderer(glamour.WithAutoStyle(), glamour.WithWordWrap(wrap)); err == nil {
		m.md = md
	}
}

func (m *model) refreshViewport() {
	m.viewport.SetContent(renderBlocks(m.history, m.md))
	m.viewport.GotoBottom()
}

func (m *model) applyEvent(e Event) {
	switch e.Kind {
	case EventAssistantText:
		m.history = append(m.history, renderedBlock{kind: blockAssistant, body: e.Text})
	case EventToolStart:
		m.history = append(m.history, renderedBlock{kind: blockTool, toolID: e.ToolID, toolName: e.ToolName, toolArgs: e.ToolInput})
	case EventToolEnd:
		for i := len(m.history) - 1; i >= 0; i-- {
			b := &m.history[i]
			if b.kind == blockTool && b.toolID == e.ToolID && !b.toolDone {
				b.body = e.ToolOutput
				b.toolDone = true
				b.isError = e.IsError
				return
			}
		}
		m.history = append(m.history, renderedBlock{kind: blockTool, toolID: e.ToolID, toolName: e.ToolName, body: e.ToolOutput, toolDone: true, isError: e.IsError})
	case EventError:
		body := "unknown error"
		if e.Err != nil {
			body = e.Err.Error()
		}
		m.history = append(m.history, renderedBlock{kind: blockError, body: body})
	case EventInfo:
		m.history = append(m.history, renderedBlock{kind: blockSystem, body: e.Text})
	case EventPlanStart:
		m.history = append(m.history, renderedBlock{kind: blockSystem, body: "Planning: " + e.Text})
	case EventPlanReady, EventPlanDone:
		m.history = append(m.history, renderedBlock{kind: blockSystem, body: e.Text})
	case EventTaskStart:
		m.history = append(m.history, renderedBlock{kind: blockSystem, body: "-> " + e.Text})
	}
}

func (m model) handleApprovalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	res, ok := decodeApprovalKey(msg, *m.pending)
	if !ok {
		return m, nil
	}
	req := *m.pending
	m.pending = nil
	m.history = append(m.history, renderedBlock{kind: blockSystem, body: approvalSummary(req, res)})
	m.refreshViewport()
	return m, tea.Batch(m.bridge.sendDecision(res), m.bridge.waitForApproval())
}

type commandInfo struct {
	Name        string
	Usage       string
	Description string
}

var commandPalette = []commandInfo{
	{Name: "/help", Usage: "/help", Description: "Show command help."},
	{Name: "/commands", Usage: "/commands [query]", Description: "Search local TUI commands."},
	{Name: "/tools", Usage: "/tools", Description: "List registered tools."},
	{Name: "/mcp", Usage: "/mcp", Description: "Show MCP server status."},
	{Name: "/plan", Usage: "/plan <goal>", Description: "Plan and execute a goal as ordered tasks."},
	{Name: "/team", Usage: "/team <goal>", Description: "Run multi-agent collaboration."},
	{Name: "/index", Usage: "/index [path]", Description: "Index code for retrieval."},
	{Name: "/memory", Usage: "/memory [status|facts|clear|forget]", Description: "Inspect or manage memory."},
	{Name: "/hitl", Usage: "/hitl [on|off|status|clear]", Description: "Configure human approval."},
	{Name: "/task", Usage: "/task [add|cancel|log] ...", Description: "Manage the persistent task queue."},
}

func (m model) handleCommandV2(text string) string {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return ""
	}
	cmd := fields[0]
	query := strings.TrimSpace(strings.TrimPrefix(text, cmd))
	sub := ""
	if len(fields) > 1 {
		sub = fields[1]
	}

	switch cmd {
	case "/help":
		return formatCommands("")
	case "/commands":
		return formatCommands(query)
	case "/mcp":
		if m.mcpStatus == nil {
			return "MCP: not configured."
		}
		return mcp.FormatStatus(m.mcpStatus())
	case "/tools":
		if m.agent == nil {
			return "No agent is configured."
		}
		tools := m.agent.ToolSummaries()
		if len(tools) == 0 {
			return "No tools are currently registered."
		}
		lines := []string{fmt.Sprintf("Registered tools: %d", len(tools))}
		for _, item := range tools {
			lines = append(lines, "  "+item)
		}
		return strings.Join(lines, "\n")
	case "/task":
		if m.taskManager == nil {
			if m.taskInitErr != "" {
				return "Task system unavailable: " + m.taskInitErr
			}
			return "Task system unavailable."
		}
		payload := strings.TrimSpace(strings.TrimPrefix(text, "/task"))
		return runtimetask.HandleCommand(m.taskManager, payload)
	case "/hitl":
		if m.hitlHandler == nil {
			return "HITL approval is not enabled."
		}
		switch sub {
		case "", "status":
			if m.hitlHandler.IsEnabled() {
				return "Tool approval: on. Mutating local tools and MCP tools require approval."
			}
			return "Tool approval: off. Tools execute without approval prompts."
		case "on":
			m.hitlHandler.SetEnabled(true)
			return "Tool approval enabled."
		case "off":
			m.hitlHandler.SetEnabled(false)
			return "Tool approval disabled."
		case "clear":
			m.hitlHandler.ClearApprovedAll()
			return "Cleared approve-all decisions for this session."
		default:
			return fmt.Sprintf("Unknown /hitl subcommand %q. Usage: /hitl [on|off|status|clear]", sub)
		}
	case "/memory":
		if m.agent == nil || !m.agent.MemoryEnabled() {
			return "Memory is not enabled."
		}
		switch sub {
		case "", "status":
			return m.agent.MemoryStatus()
		case "facts":
			facts := m.agent.MemoryFacts()
			if len(facts) == 0 {
				return "Long-term memory has no facts."
			}
			lines := make([]string, 0, len(facts))
			for i, f := range facts {
				lines = append(lines, fmt.Sprintf("%d. %s", i+1, f))
			}
			return strings.Join(lines, "\n")
		case "clear":
			m.agent.ClearShortTermMemory()
			return "Cleared short-term memory for this conversation."
		case "forget":
			m.agent.ClearLongTermMemory()
			return "Cleared persistent long-term memory."
		default:
			return fmt.Sprintf("Unknown /memory subcommand %q. Use /commands memory.", sub)
		}
	default:
		return fmt.Sprintf("Unknown command %q. Use /commands to search available commands.", cmd)
	}
}

func (m model) commandMenuMatches() []commandInfo {
	raw := m.input.Value()
	if !strings.HasPrefix(raw, "/") || strings.ContainsAny(raw, " \t\r\n") {
		return nil
	}
	query := strings.ToLower(strings.TrimPrefix(raw, "/"))
	matches := make([]commandInfo, 0, len(commandPalette))
	seen := make(map[string]bool, len(commandPalette))
	for _, command := range commandPalette {
		name := strings.TrimPrefix(strings.ToLower(command.Name), "/")
		if query == "" || strings.HasPrefix(name, query) {
			matches = append(matches, command)
			seen[command.Name] = true
		}
	}
	if query != "" {
		for _, command := range commandPalette {
			if seen[command.Name] {
				continue
			}
			haystack := strings.ToLower(command.Name + " " + command.Usage + " " + command.Description)
			if strings.Contains(haystack, query) {
				matches = append(matches, command)
				seen[command.Name] = true
			}
		}
	}
	if len(matches) > maxCommandMenuItems {
		return matches[:maxCommandMenuItems]
	}
	return matches
}

func (m *model) clampCommandMenuIndex() {
	matches := m.commandMenuMatches()
	if len(matches) == 0 {
		m.commandMenuIndex = 0
		return
	}
	if m.commandMenuIndex < 0 {
		m.commandMenuIndex = len(matches) - 1
	}
	if m.commandMenuIndex >= len(matches) {
		m.commandMenuIndex = 0
	}
}

func (m model) renderCommandMenu() string {
	matches := m.commandMenuMatches()
	if len(matches) == 0 {
		return ""
	}
	index := m.commandMenuIndex
	if index < 0 || index >= len(matches) {
		index = 0
	}
	lines := []string{"Commands"}
	for i, command := range matches {
		prefix := "  "
		style := commandMenuItemStyle
		if i == index {
			prefix = "> "
			style = commandMenuSelectedStyle
		}
		lines = append(lines, style.Render(fmt.Sprintf("%s%-22s %s", prefix, command.Usage, command.Description)))
	}
	lines = append(lines, commandMenuHintStyle.Render("  up/down select  tab complete  enter complete/run"))
	return commandMenuStyle.Render(strings.Join(lines, "\n"))
}

func (m model) handleCommandMenuKey(msg tea.KeyMsg) (model, bool) {
	matches := m.commandMenuMatches()
	if len(matches) == 0 {
		return m, false
	}
	m.clampCommandMenuIndex()
	switch msg.Type {
	case tea.KeyUp:
		m.commandMenuIndex--
		m.clampCommandMenuIndex()
		return m, true
	case tea.KeyDown:
		m.commandMenuIndex++
		m.clampCommandMenuIndex()
		return m, true
	case tea.KeyTab:
		m.acceptCommandMenuSelection(matches)
		return m, true
	case tea.KeyEnter:
		current := strings.TrimSpace(m.input.Value())
		selected := matches[m.commandMenuIndex]
		if current != selected.Name {
			m.acceptCommandMenuSelection(matches)
			return m, true
		}
	}
	return m, false
}

func (m *model) acceptCommandMenuSelection(matches []commandInfo) {
	if len(matches) == 0 {
		return
	}
	m.clampCommandMenuIndex()
	if m.commandMenuIndex >= len(matches) {
		m.commandMenuIndex = 0
	}
	m.input.SetValue(matches[m.commandMenuIndex].Name + " ")
	m.input.CursorEnd()
}

func formatCommands(query string) string {
	query = strings.ToLower(strings.TrimSpace(query))
	lines := []string{"Available commands:"}
	matches := 0
	for _, command := range commandPalette {
		haystack := strings.ToLower(command.Name + " " + command.Usage + " " + command.Description)
		if query != "" && !strings.Contains(haystack, query) {
			continue
		}
		matches++
		lines = append(lines, fmt.Sprintf("  %-30s %s", command.Usage, command.Description))
	}
	if matches == 0 {
		return fmt.Sprintf("No commands match %q.", query)
	}
	lines = append(lines, "", "Attachments: @image:<path>, @clipboard")
	return strings.Join(lines, "\n")
}

func (m model) runPlan(goal string, events chan Event) tea.Cmd {
	return func() tea.Msg {
		text, err := m.agent.Plan(context.Background(), goal)
		close(events)
		return planDoneMsg{text: text, err: err}
	}
}

func (m model) runTeam(goal string, events chan Event) tea.Cmd {
	return func() tea.Msg {
		text, err := m.agent.RunTeam(context.Background(), goal)
		close(events)
		return planDoneMsg{text: text, err: err}
	}
}

func (m model) runIndex(path string) tea.Cmd {
	return func() tea.Msg {
		text, err := m.agent.IndexProject(context.Background(), path)
		return indexDoneMsg{text: text, err: err}
	}
}

func (m model) runAgent(input string, events chan Event) tea.Cmd {
	return func() tea.Msg {
		text, err := m.agent.Run(context.Background(), input)
		close(events)
		return agentDoneMsg{text: text, err: err}
	}
}

func (m model) waitForEvent(events chan Event) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-events
		if !ok {
			return nil
		}
		return eventMsg(e)
	}
}

func (m model) tokenUsage() string {
	if m.agent == nil {
		return ""
	}
	return m.agent.TokenUsageCompact()
}
