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

func (m *MCPManager) StatusSnapshot() StatusSnapshot {
	if m == nil {
		return StatusSnapshot{}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()

	byName := make(map[string]*ServerStatus, len(m.servers))
	for name := range m.servers {
		byName[name] = &ServerStatus{Name: name}
	}
	for _, entry := range m.tools {
		status := ensureServerStatus(byName, entry.serverName)
		status.Tools++
	}
	for _, entry := range m.resources {
		status := ensureServerStatus(byName, entry.serverName)
		status.Resources++
	}
	for _, entry := range m.prompts {
		status := ensureServerStatus(byName, entry.serverName)
		status.Prompts++
	}

	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	out := StatusSnapshot{Servers: make([]ServerStatus, 0, len(names))}
	for _, name := range names {
		out.Servers = append(out.Servers, *byName[name])
	}
	return out
}

func FormatStatus(snapshot StatusSnapshot) string {
	if len(snapshot.Servers) == 0 {
		return "MCP: no connected servers."
	}
	lines := []string{fmt.Sprintf("MCP servers: %d", len(snapshot.Servers))}
	for _, server := range snapshot.Servers {
		lines = append(lines, fmt.Sprintf(
			"  %s: %d tools, %d resources, %d prompts",
			server.Name,
			server.Tools,
			server.Resources,
			server.Prompts,
		))
	}
	return strings.Join(lines, "\n")
}

func ensureServerStatus(status map[string]*ServerStatus, name string) *ServerStatus {
	if existing := status[name]; existing != nil {
		return existing
	}
	created := &ServerStatus{Name: name}
	status[name] = created
	return created
}
