package tui

import "github.com/charmbracelet/lipgloss"

// 配色：暖底 + 三种角色色，沿用 Charm 风。
var (
	colorUser      = lipgloss.Color("#7AA2F7") // 蓝
	colorAssistant = lipgloss.Color("#9ECE6A") // 绿
	colorTool      = lipgloss.Color("#E0AF68") // 橙
	colorErr       = lipgloss.Color("#F7768E") // 红
	colorMuted     = lipgloss.Color("#565F89") // 浅灰蓝
	colorAccent    = lipgloss.Color("#BB9AF7") // 紫
)

// 三种角色的 prefix bar，竖条 + 名字。
var (
	userBarStyle = lipgloss.NewStyle().
			Foreground(colorUser).
			Bold(true)

	assistantBarStyle = lipgloss.NewStyle().
				Foreground(colorAssistant).
				Bold(true)

	toolBarStyle = lipgloss.NewStyle().
			Foreground(colorTool).
			Bold(true)

	errBarStyle = lipgloss.NewStyle().
			Foreground(colorErr).
			Bold(true)

	systemBarStyle = lipgloss.NewStyle().
			Foreground(colorMuted).
			Bold(true)

	// HITL 审批卡片:醒目的紫色标题 + 暗色元信息 + 橙色按键提示。
	approvalBarStyle = lipgloss.NewStyle().
				Foreground(colorAccent).
				Bold(true)

	approvalMetaStyle = lipgloss.NewStyle().
				Foreground(colorMuted).
				PaddingLeft(2)

	approvalHintStyle = lipgloss.NewStyle().
				Foreground(colorTool).
				Bold(true).
				PaddingLeft(2)
)

// 块正文样式：每行前面缩进两个空格，让 prefix bar 视觉对齐。
var bodyStyle = lipgloss.NewStyle().PaddingLeft(2)

// 工具调用参数和输出的样式：暗一点的 mono 块。
var (
	toolArgStyle = lipgloss.NewStyle().
			Foreground(colorMuted).
			PaddingLeft(2)

	toolOutputStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#A9B1D6")).
			PaddingLeft(2)
)

// 顶部标题栏 + 底部帮助栏。
var (
	headerStyle = lipgloss.NewStyle().
			Foreground(colorAccent).
			Bold(true).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorMuted).
			Padding(0, 1)

	helpStyle = lipgloss.NewStyle().
			Foreground(colorMuted).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorMuted).
			Padding(0, 1)
)

// viewport 外框 / 输入框外框。
var (
	viewportStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorMuted).
			Padding(0, 1)

	inputStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorAccent).
			Padding(0, 1)
)

// Slash command menu shown above the input when the user types "/".
var (
	commandMenuStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorMuted).
				Padding(0, 1)

	commandMenuItemStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#A9B1D6"))

	commandMenuSelectedStyle = lipgloss.NewStyle().
					Foreground(colorAccent).
					Bold(true)

	commandMenuHintStyle = lipgloss.NewStyle().
				Foreground(colorMuted)
)
