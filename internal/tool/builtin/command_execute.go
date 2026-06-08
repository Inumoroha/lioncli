package builtin

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"

	"lioncli/internal/tool"
)

func init() {
	tool.RegisterToolAuto(newExecuteCommandTool())
}

func newExecuteCommandTool() tool.Tool {
	return tool.Tool{
		Name:        "command_execute",
		Description: "Execute a shell command for compilation, running programs, or local repo operations.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "Shell command to execute.",
				},
			},
			"required": []string{"command"},
		},
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			// 1. 从参数中获取命令字符串，如果缺失则返回错误。
			commandStr := tool.StringArg(args, "command")
			if commandStr == "" {
				return "", fmt.Errorf("命令不能为空")
			}

			// 2. 执行命令并获取输出。
			var cmd *exec.Cmd
			if runtime.GOOS == "windows" {
				cmd = exec.CommandContext(ctx, "cmd", "/C", commandStr)
			} else {
				cmd = exec.CommandContext(ctx, "sh", "-c", commandStr)
			}
			outBytes, err := cmd.CombinedOutput()

			exitCode := 0
			if cmd.ProcessState != nil {
				exitCode = cmd.ProcessState.ExitCode()
			}

			if err != nil {
				return fmt.Sprintf(
					"命令执行失败 (退出码: %d, err: %v)\n输出:\n%s",
					exitCode, err, string(outBytes),
				), nil
			}
			return fmt.Sprintf(
				"命令执行完成 (退出码: %d)\n输出:\n%s",
				exitCode, string(outBytes),
			), nil
		},
	}
}
