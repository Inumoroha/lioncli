package skill

import (
	"embed"
	"io/fs"
)

// builtinFS 把 builtin/ 整棵目录树编译进二进制——Go 版的"打进 jar"。
// 用 all: 前缀,确保 _ / . 开头的文件也被包含(默认会被 embed 忽略)。
//
//go:embed all:builtin
var builtinFS embed.FS

// builtinRoot 是 embed 树里内置技能目录的根。
const builtinRoot = "builtin"

// BuiltinFS 返回内置技能的只读文件系统,供 main.go 装配 SkillRegistry。
func BuiltinFS() fs.FS {
	return builtinFS
}

// BuiltinRoot 返回内置技能在 BuiltinFS() 里的根目录名。
func BuiltinRoot() string {
	return builtinRoot
}
