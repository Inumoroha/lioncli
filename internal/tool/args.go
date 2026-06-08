package tool

import "fmt"

// StringArg 从 LLM/MCP 传过来的 map[string]any 里抽 string 参数。
// LLM 偶尔会把 number/bool 也塞给字符串字段，这里宽松处理一下。
// 导出供 builtin 子包等包外的工具实现复用。
func StringArg(args map[string]any, key string) string {
	v, ok := args[key]
	if !ok || v == nil {
		return ""
	}
	switch s := v.(type) {
	case string:
		return s
	case fmt.Stringer:
		return s.String()
	default:
		return fmt.Sprint(v)
	}
}

// IntArg 从 map 里抽 int 参数，取不到/类型不符时返回 def。
// JSON 反序列化后数字默认是 float64，LLM 也可能塞字符串，这里一并兼容。
// 导出供 builtin 子包等包外的工具实现复用。
func IntArg(args map[string]any, key string, def int) int {
	v, ok := args[key]
	if !ok || v == nil {
		return def
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	case string:
		var i int
		if _, err := fmt.Sscanf(n, "%d", &i); err == nil {
			return i
		}
	}
	return def
}