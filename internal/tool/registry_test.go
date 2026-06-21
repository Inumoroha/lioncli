package tool

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

// stubTool 构造一个带固定返回值的工具，方便在测试里注册和分发。
func stubTool(name, result string) Tool {
	return Tool{
		Name:        name,
		Description: "desc-" + name,
		Parameters:  map[string]any{"type": "object"},
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return result, nil
		},
	}
}

func TestRegisterToolValidation(t *testing.T) {
	r := &ToolRegistry{tools: map[string]Tool{}}

	if err := r.RegisterTool(Tool{Name: "", Execute: stubTool("x", "").Execute}); err == nil {
		t.Error("空名字应返回错误")
	}
	if err := r.RegisterTool(Tool{Name: "noexec", Execute: nil}); err == nil {
		t.Error("缺少执行函数应返回错误")
	}

	if err := r.RegisterTool(stubTool("ok", "r")); err != nil {
		t.Fatalf("合法工具注册失败: %v", err)
	}
	if !r.IsRegistered("ok") {
		t.Error("注册后 IsRegistered 应为 true")
	}

	// 重名不能静默覆盖。
	if err := r.RegisterTool(stubTool("ok", "other")); err == nil {
		t.Error("重名注册应返回错误")
	}
}

func TestGetAllToolsSorted(t *testing.T) {
	r := &ToolRegistry{tools: map[string]Tool{}}
	for _, name := range []string{"charlie", "alpha", "bravo"} {
		if err := r.RegisterTool(stubTool(name, "")); err != nil {
			t.Fatal(err)
		}
	}

	got := r.GetAllTools()
	want := []string{"alpha", "bravo", "charlie"}
	if len(got) != len(want) {
		t.Fatalf("工具数量 = %d, want %d", len(got), len(want))
	}
	for i, name := range want {
		if got[i].Name != name {
			t.Errorf("第 %d 个工具 = %q, want %q（应按名字排序）", i, got[i].Name, name)
		}
	}
}

func TestToLLMToolsDropsExecutor(t *testing.T) {
	r := &ToolRegistry{tools: map[string]Tool{}}
	if err := r.RegisterTool(stubTool("calc", "")); err != nil {
		t.Fatal(err)
	}

	llmTools := r.ToLLMTools()
	if len(llmTools) != 1 {
		t.Fatalf("ToLLMTools 数量 = %d, want 1", len(llmTools))
	}
	lt := llmTools[0]
	if lt.Name != "calc" || lt.Description != "desc-calc" {
		t.Errorf("name/description 未正确传递: %+v", lt)
	}
	if lt.Parameters == nil {
		t.Error("Parameters 应被保留")
	}
}

func TestExecuteTool(t *testing.T) {
	r := &ToolRegistry{tools: map[string]Tool{}}
	if err := r.RegisterTool(stubTool("echo", "pong")); err != nil {
		t.Fatal(err)
	}

	// 正常分发。
	out, err := r.ExecuteTool(context.Background(), "echo", nil)
	if err != nil {
		t.Fatalf("ExecuteTool 返回错误: %v", err)
	}
	if out != "pong" {
		t.Errorf("ExecuteTool 输出 = %q, want %q", out, "pong")
	}

	// 工具不存在。
	if _, err := r.ExecuteTool(context.Background(), "missing", nil); err == nil {
		t.Error("调用未注册工具应返回错误")
	}

	// 执行函数自身的错误应原样向上传递。
	sentinel := errors.New("boom")
	if err := r.RegisterTool(Tool{
		Name: "fail",
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return "", sentinel
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ExecuteTool(context.Background(), "fail", nil); !errors.Is(err, sentinel) {
		t.Errorf("执行错误未透传, got %v", err)
	}
}

func TestExecuteToolPassesArgs(t *testing.T) {
	r := &ToolRegistry{tools: map[string]Tool{}}
	if err := r.RegisterTool(Tool{
		Name: "greet",
		Execute: func(_ context.Context, args map[string]any) (string, error) {
			return "hi " + StringArg(args, "name"), nil
		},
	}); err != nil {
		t.Fatal(err)
	}

	out, err := r.ExecuteTool(context.Background(), "greet", map[string]any{"name": "lion"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "hi lion" {
		t.Errorf("参数未正确传入执行函数, got %q", out)
	}
}

func TestUnregisterToolsByPrefix(t *testing.T) {
	r := &ToolRegistry{tools: map[string]Tool{}}
	for _, name := range []string{"mcp_a", "mcp_b", "local_c"} {
		if err := r.RegisterTool(stubTool(name, "")); err != nil {
			t.Fatal(err)
		}
	}

	if n := r.UnregisterToolsByPrefix(""); n != 0 {
		t.Errorf("空前缀应返回 0, got %d", n)
	}

	n := r.UnregisterToolsByPrefix("mcp_")
	if n != 2 {
		t.Errorf("删除条数 = %d, want 2", n)
	}
	if r.IsRegistered("mcp_a") || r.IsRegistered("mcp_b") {
		t.Error("mcp_ 前缀的工具应被删除")
	}
	if !r.IsRegistered("local_c") {
		t.Error("不匹配前缀的工具应保留")
	}
}

// TestRegistryConcurrentAccess 在 -race 下验证读写锁的正确性：
// 多个 goroutine 同时注册、读取、执行不应触发数据竞争。
func TestRegistryConcurrentAccess(t *testing.T) {
	r := &ToolRegistry{tools: map[string]Tool{}}
	var wg sync.WaitGroup

	for i := range 20 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = r.RegisterTool(stubTool(fmt.Sprintf("t%d", i), "ok"))
			_ = r.GetAllTools()
			_, _ = r.ExecuteTool(context.Background(), fmt.Sprintf("t%d", i), nil)
		}(i)
	}
	wg.Wait()

	if got := len(r.GetAllTools()); got != 20 {
		t.Errorf("并发注册后工具数 = %d, want 20", got)
	}
}
