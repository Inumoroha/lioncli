package builtin

import (
	"context"
	"fmt"
	"os"

	"lioncli/internal/tool"
)

func init() {
	tool.RegisterToolAuto(newFileReadTool())
}

func newFileReadTool() tool.Tool {
	return tool.Tool{
		Name:        "read_file",
		Description: "Read a file so the agent can inspect code, config, or text content.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Absolute or relative file path to read.",
				},
			},
			"required": []string{"path"},
		},
		Execute: func(_ context.Context, args map[string]any) (string, error) {
			path := tool.StringArg(args, "path")
			if path == "" {
				return "", fmt.Errorf("missing required parameter: path")
			}

			content, err := os.ReadFile(path)
			if err != nil {
				// 让 LLM 看到错误原因，不直接 return error 中断对话循环。
				return fmt.Sprintf(
					"failed to read file %q: %v\nCheck whether the path is correct or inspect the directory structure first.",
					path, err,
				), nil
			}

			s := string(content)
			if len(s) > 20000 {
				s = s[:20000] + "\n\n...[truncated]..."
			}
			return fmt.Sprintf("Contents of %q:\n%s", path, s), nil
		},
	}
}
