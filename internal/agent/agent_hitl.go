package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"lioncli/internal/hitl"
)

func (a *Agent) approveTool(name string, args *map[string]any) (string, bool) {
	argsJSON, err := json.Marshal(*args)
	if err != nil {
		argsJSON = []byte("{}")
	}

	effective, denyMsg, blocked := hitl.ApplyGateWithContext(
		a.gate,
		a.browserGuard,
		name,
		string(argsJSON),
		"",
		approvalWorkspaceContext(name),
	)
	if blocked {
		return denyMsg, true
	}

	if effective != string(argsJSON) {
		var modified map[string]any
		if err := json.Unmarshal([]byte(effective), &modified); err == nil {
			*args = modified
		}
	}
	return "", false
}

func approvalWorkspaceContext(toolName string) string {
	switch toolName {
	case "apply_patch", "write_file", "command_execute", "git_commit":
	default:
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	cmd := exec.CommandContext(ctx, "git", "-C", cwd, "status", "--short", "--branch")
	raw, err := cmd.Output()
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return ""
	}
	if len(lines) > 8 {
		omitted := len(lines) - 8
		lines = append(lines[:8], fmt.Sprintf("...(%d more lines)", omitted))
	}
	return strings.Join(lines, " | ")
}

func (a *Agent) afterToolExecution(name string, args map[string]any, output string) {
	if a.browserGuard == nil {
		return
	}
	argsJSON, err := json.Marshal(args)
	if err != nil {
		return
	}
	a.browserGuard.AfterExecution(name, string(argsJSON), output)
}
