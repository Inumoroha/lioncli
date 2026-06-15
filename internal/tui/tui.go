package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"lioncli/internal/mcp"
)

func Run(a Agent, modelName string, mcpStatus func() mcp.StatusSnapshot) error {
	m := newModel(a, modelName, mcpStatus)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}
