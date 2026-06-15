package prompt

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
)

// builtinFS 把 builtin/ 整棵片段树编译进二进制。用 all: 前缀确保 _ / .
// 开头的文件也被包含(默认会被 embed 忽略)。
//
//go:embed all:builtin
var builtinFS embed.FS

// BuiltinFS 返回内置提示词片段的只读文件系统。
func BuiltinFS() fs.FS {
	return builtinFS
}

// DefaultRepository 用内置片段 + teacli 约定的覆盖目录构造 Repository:
// 用户层 <UserConfigDir>/teacli/prompts,项目层 ./.teacli/prompts。
// 取不到用户配置目录时该覆盖层关闭(传空),不影响内置加载。
func DefaultRepository() *Repository {
	userDir := ""
	if dir, err := os.UserConfigDir(); err == nil {
		userDir = filepath.Join(dir, "teacli", "prompts")
	}
	projectDir := filepath.Join(".teacli", "prompts")
	return NewRepository(builtinFS, userDir, projectDir)
}

// NewDefaultAssembler 返回基于 DefaultRepository 的 Assembler。
func NewDefaultAssembler() *Assembler {
	return NewAssembler(DefaultRepository())
}
