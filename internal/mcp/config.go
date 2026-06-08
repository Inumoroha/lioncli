package mcp

// MCPServerConfig 描述单个 MCP server 的接入方式。两种 transport 共用同一结构，
// 通过 Type 显式声明；未填则按 URL 是否存在自动推断（有 URL = http，否则 stdio）。
type MCPServerConfig struct {
	// Type 取值："stdio" | "http" | "streamable-http"。后两者等价，都走 Streamable HTTP。
	// 留空时按 URL 是否存在推断，便于已有 stdio 配置无感升级。
	Type string `json:"type,omitempty"`

	// stdio 专用
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`

	// streamable-http 专用
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

type MCPConfig struct {
	MCPServers map[string]MCPServerConfig `json:"mcpServers"`
}
