package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"lioncli/internal/mcp"
)

func Run(a Agent, modelName string, mcpStatus func() mcp.StatusSnapshot) error {
	m := newModel(a, modelName, mcpStatus)
	// 捕获鼠标事件，使滚轮可以滚动聊天区（查看 AI 的长段回复）。
	// 否则在全屏模式下终端会把滚轮转换成上/下方向键，被输入历史导航吞掉。
	// 需要用终端原生鼠标选择文本时，按住 Shift 拖动即可（再用 Ctrl+Shift+C 复制）。
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}
