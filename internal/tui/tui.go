package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"lioncli/internal/mcp"
)

func Run(a Agent, modelName string, mcpStatus func() mcp.StatusSnapshot) error {
	m := newModel(a, modelName, mcpStatus)
	// 不捕获鼠标事件，让用户可以用终端的原生鼠标选择功能
	// 用户可以按 Shift+鼠标 或 Ctrl+Shift+鼠标 来选择文本，然后用 Ctrl+Shift+C 复制
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}
