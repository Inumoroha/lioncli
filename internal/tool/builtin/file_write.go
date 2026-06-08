package builtin

import (
	"context"
	"fmt"
	"os"

	"lioncli/internal/tool"
)

func init() {
	tool.RegisterToolAuto(newWriteFileTool())
}

func newWriteFileTool() tool.Tool {
	return tool.Tool{
		Name:        "write_file",
		Description: "Write content into a file, useful for generating code or saving config.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Destination file path.",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "File contents to write.",
				},
			},
			"required": []string{"path", "content"},
		},
		Execute: func(_ context.Context, args map[string]any) (string, error) {
			path := tool.StringArg(args, "path")
			content := tool.StringArg(args, "content")
			if path == "" {
				return "", fmt.Errorf("missing required parameter: path")
			}

			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				// 让 LLM 看到错误原因，不直接 return error 中断对话循环。
				return fmt.Sprintf(
					"写入文件失败 %q: %v\n检查路径是否正确或目录是否存在。",
					path, err,
				), nil
			}
			
			return fmt.Sprintf("写入文件成功，共写入了 %d 字节", len(content)), nil
		},
	}
}
