package tool

import (
	"fmt"
	"sort"
	"sync"
)

// 包级自动注册表。本地工具在自己的文件里用 init() + RegisterAuto 落地。
// NewToolRegistry 时会把这里的所有工具拷贝到 ToolRegistry 实例。
var (
	autoMu    sync.Mutex
	autoTools = make(map[string]Tool)
)

// RegisterAuto 在包初始化阶段把一个工具登记进自动注册表。
// 重名直接 panic，防止启动时静默覆盖。
func RegisterToolAuto(t Tool) {
	autoMu.Lock()
	defer autoMu.Unlock()
	if _, exists := autoTools[t.Name]; exists {
		panic(fmt.Sprintf("【🔧】❗ 工具 %q 已自动注册", t.Name))
	}
	autoTools[t.Name] = t
}

// sortedToolsAuto 返回按名字排序的自动注册工具拷贝。
func sortedToolsAuto() []Tool {
	autoMu.Lock()
	defer autoMu.Unlock()

	// 按名字排序
	names := make([]string, 0, len(autoTools))
	for name := range autoTools {
		names = append(names, name)
	}
	sort.Strings(names)

	// 按名字排序的工具拷贝
	out := make([]Tool, 0, len(names))
	for _, name := range names {
		out = append(out, autoTools[name])
	}
	return out
}