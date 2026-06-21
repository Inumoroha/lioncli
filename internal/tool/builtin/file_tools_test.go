package builtin

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteThenReadRoundTrip 验证 write_file 写入的内容能被 read_file 原样读回。
func TestWriteThenReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	content := "hello\n第二行\n"

	write := newWriteFileTool().Execute
	read := newFileReadTool().Execute
	ctx := context.Background()

	out, err := write(ctx, map[string]any{"path": path, "content": content})
	if err != nil {
		t.Fatalf("write_file 返回错误: %v", err)
	}
	if !strings.Contains(out, "成功") {
		t.Errorf("write_file 输出未表示成功: %q", out)
	}

	out, err = read(ctx, map[string]any{"path": path})
	if err != nil {
		t.Fatalf("read_file 返回错误: %v", err)
	}
	if !strings.Contains(out, content) {
		t.Errorf("read_file 未读回原始内容, got %q", out)
	}
}

// TestFileToolsMissingPath 验证缺少必填 path 参数时返回错误。
func TestFileToolsMissingPath(t *testing.T) {
	ctx := context.Background()

	if _, err := newWriteFileTool().Execute(ctx, map[string]any{"content": "x"}); err == nil {
		t.Error("write_file 缺少 path 应返回错误")
	}
	if _, err := newFileReadTool().Execute(ctx, map[string]any{}); err == nil {
		t.Error("read_file 缺少 path 应返回错误")
	}
}

// TestReadMissingFile 验证读取不存在的文件不会中断对话循环：
// 返回的是给 LLM 的提示文本而非 Go error。
func TestReadMissingFile(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "does-not-exist.txt")

	out, err := newFileReadTool().Execute(context.Background(), map[string]any{"path": missing})
	if err != nil {
		t.Fatalf("读取不存在文件不应返回 Go error（应作为文本反馈给 LLM）, got %v", err)
	}
	if !strings.Contains(out, "failed to read file") {
		t.Errorf("输出应包含失败提示, got %q", out)
	}
}
