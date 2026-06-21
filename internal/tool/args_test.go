package tool

import "testing"

// stringer 用来验证 StringArg 对 fmt.Stringer 的分支。
type stringer struct{ s string }

func (s stringer) String() string { return s.s }

func TestStringArg(t *testing.T) {
	args := map[string]any{
		"str":      "hello",
		"stringer": stringer{s: "via-stringer"},
		"int":      7,
		"float":    3.5,
		"bool":     true,
		"nil":      nil,
	}

	cases := []struct {
		name string
		key  string
		want string
	}{
		{"普通字符串", "str", "hello"},
		{"实现了 Stringer", "stringer", "via-stringer"},
		{"int 兜底转字符串", "int", "7"},
		{"float64 兜底转字符串", "float", "3.5"},
		{"bool 兜底转字符串", "bool", "true"},
		{"值为 nil 返回空串", "nil", ""},
		{"键不存在返回空串", "missing", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := StringArg(args, tc.key); got != tc.want {
				t.Errorf("StringArg(%q) = %q, want %q", tc.key, got, tc.want)
			}
		})
	}
}

func TestIntArg(t *testing.T) {
	const def = -1
	args := map[string]any{
		"float":    float64(42), // JSON 反序列化后数字默认是 float64
		"floatcut": 3.9,         // 截断而非四舍五入
		"int":      10,
		"int64":    int64(99),
		"numstr":   "256",
		"badstr":   "not-a-number",
		"bool":     true, // 不支持的类型应回落到默认值
		"nil":      nil,
	}

	cases := []struct {
		name string
		key  string
		want int
	}{
		{"float64 转 int", "float", 42},
		{"float64 向零截断", "floatcut", 3},
		{"原生 int", "int", 10},
		{"int64 转 int", "int64", 99},
		{"数字字符串", "numstr", 256},
		{"非法字符串回落默认值", "badstr", def},
		{"不支持的类型回落默认值", "bool", def},
		{"nil 回落默认值", "nil", def},
		{"键不存在回落默认值", "missing", def},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IntArg(args, tc.key, def); got != tc.want {
				t.Errorf("IntArg(%q) = %d, want %d", tc.key, got, tc.want)
			}
		})
	}
}
