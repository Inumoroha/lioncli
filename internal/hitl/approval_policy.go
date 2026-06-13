package hitl

import (
	"sort"
	"strings"
)

// mcpPrefix / mcpSep 是 MCP 工具名的前缀与分隔符,统一为 mcp__<server>__<tool>。
// 与 internal/mcp 的 KeyPrefix 保持一致;此处单独定义一份常量(而非 import mcp)
// 是为了不把 MCP 客户端的第三方依赖拖进底层审批包。改前缀只需动这两个常量。
const (
	mcpPrefix = "mcp__"
	mcpSep    = "__"
)

// dangerousTools 列出本地需要人工审批的高/中危工具。
// 必须与本项目真实注册的工具名严格一致:执行 shell 的工具叫 command_execute
// (不是 execute_command),拼错会导致最危险的工具漏审、被静默放行。
// 读文件/检索/web 抓取等只读或低危工具不在此列,放行不打断。
// 任何 MCP 工具(mcp__ 前缀)由 RequiresApproval 单独纳入审批。
//
// apply_patch / git_commit 目前项目尚未注册,作为常见危险动作前瞻保留:
// 不存在则永不匹配(无害),日后注册即自动纳入审批。安全名单宁可多列不可漏列。
var dangerousTools = map[string]struct{}{
	"command_execute": {},
	"write_file":      {},
	"apply_patch":     {},
	"git_commit":      {},
}

func RequiresApproval(toolName string) bool {
	_, ok := dangerousTools[toolName]
	return ok || IsMCPTool(toolName)
}

func DangerLevel(toolName string) string {
	switch toolName {
	case "command_execute", "git_commit":
		return "HIGH"
	case "apply_patch", "write_file":
		return "MEDIUM"
	default:
		if IsMCPTool(toolName) {
			return "MCP"
		}
		return "SAFE"
	}
}

func RiskDescription(toolName string) string {
	switch toolName {
	case "command_execute":
		return "Runs a shell command that may modify files, install software, or affect the host system."
	case "git_commit":
		return "Creates a Git commit and modifies repository history."
	case "apply_patch":
		return "Modifies files in the workspace by applying a patch."
	case "write_file":
		return "Writes or overwrites file contents."
	default:
		if IsMCPTool(toolName) {
			return "Calls an external MCP server that may access the network, files, or third-party services."
		}
		return "Read-only operation."
	}
}

func DangerousTools() []string {
	tools := make([]string, 0, len(dangerousTools))
	for tool := range dangerousTools {
		tools = append(tools, tool)
	}
	sort.Strings(tools)
	return tools
}

// IsMCPTool 判断工具名是否带 MCP 前缀(mcp__server__tool)。
func IsMCPTool(toolName string) bool {
	return strings.HasPrefix(toolName, mcpPrefix) && len(toolName) > len(mcpPrefix)
}

// MCPServerName 从 mcp__<server>__<tool> 提取 server 段;非 MCP 工具或无 server 段时返回 ""。
func MCPServerName(toolName string) string {
	if !IsMCPTool(toolName) {
		return ""
	}
	server, _, ok := strings.Cut(toolName[len(mcpPrefix):], mcpSep)
	if !ok {
		return ""
	}
	return server
}
