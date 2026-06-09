package mcp

import (
	"fmt"
	"sort"
	"strings"
)

type ServerStatus struct {
	Name      string
	Tools     int
	Resources int
	Prompts   int
}

type StatusSnapshot struct {
	Servers []ServerStatus
}

// StatusSnapshot 获取 MCPManager 当前状态的快照
// 该方法是线程安全的，通过读锁保护共享数据
// 返回值: 包含所有已连接服务器状态的快照，按服务器名称排序
func (m *MCPManager) StatusSnapshot() StatusSnapshot {
	// 处理 nil 接收器，返回空快照
	if m == nil {
		return StatusSnapshot{}
	}
	// 获取读锁，确保并发安全
	m.mu.RLock()
	defer m.mu.RUnlock()

	// 初始化服务器状态映射，预分配容量以优化性能
	byName := make(map[string]*ServerStatus, len(m.servers))
	for name := range m.servers {
		byName[name] = &ServerStatus{Name: name}
	}

	// 统计每个服务器的工具数量
	for _, entry := range m.tools {
		status := ensureServerStatus(byName, entry.serverName)
		status.Tools++
	}

	// 统计每个服务器的资源数量
	for _, entry := range m.resources {
		status := ensureServerStatus(byName, entry.serverName)
		status.Resources++
	}

	// 统计每个服务器的提示词数量
	for _, entry := range m.prompts {
		status := ensureServerStatus(byName, entry.serverName)
		status.Prompts++
	}

	// 提取服务器名称并按字母排序
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)

	// 构建有序的结果切片
	out := StatusSnapshot{Servers: make([]ServerStatus, 0, len(names))}
	for _, name := range names {
		out.Servers = append(out.Servers, *byName[name])
	}

	return out
}

// FormatStatus 将 MCP 服务器状态快照格式化为人类可读的字符串
// 参数 snapshot: 包含所有已连接服务器状态的快照
// 返回值: 格式化后的状态信息字符串
func FormatStatus(snapshot StatusSnapshot) string {
	// 如果没有已连接的服务器，返回提示信息
	if len(snapshot.Servers) == 0 {
		return "MCP: no connected servers."
	}
	// 创建结果行列表，首行显示服务器总数
	lines := []string{fmt.Sprintf("MCP servers: %d", len(snapshot.Servers))}
	// 遍历每个服务器，格式化输出其详细信息
	for _, server := range snapshot.Servers {
		lines = append(lines, fmt.Sprintf(
			"  %s: %d tools, %d resources, %d prompts",
			server.Name,
			server.Tools,
			server.Resources,
			server.Prompts,
		))
	}
	// 将所有行用换行符连接并返回
	return strings.Join(lines, "\n")
}

// ensureServerStatus 确保指定名称的 ServerStatus 存在于 map 中
// 采用 get-or-create 模式：如果已存在则返回现有实例，否则创建新实例并添加到 map
// 参数 status: 存储 ServerStatus 的 map
// 参数 name: 服务器名称
// 返回值: 对应的 ServerStatus 指针（可能是已有或新建的）
func ensureServerStatus(status map[string]*ServerStatus, name string) *ServerStatus {
	// 检查是否已存在该服务器的状态记录
	if existing := status[name]; existing != nil {
		return existing
	}
	// 创建新的 ServerStatus 实例
	created := &ServerStatus{Name: name}
	// 将新实例添加到 map 中
	status[name] = created
	return created
}