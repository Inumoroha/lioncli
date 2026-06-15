package prompt

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// builtinPrefix 是内置片段在 embed.FS 里的根目录前缀。
const builtinPrefix = "builtin"

// Repository 按"内置 < 用户目录 < 项目目录"三层加载提示词片段:
// 内置随二进制 embed,用户/项目目录里的同名文件依次覆盖。对齐 Java
// PromptRepository,但 classpath 资源换成 fs.FS,异常换成 error。
type Repository struct {
	builtinFS  fs.FS
	userDir    string // 为空表示该覆盖层不启用
	projectDir string
}

// NewRepository 用给定的内置文件系统与覆盖目录构造 Repository。
// userDir/projectDir 传空字符串即关闭对应覆盖层。
func NewRepository(builtinFS fs.FS, userDir, projectDir string) *Repository {
	return &Repository{
		builtinFS:  builtinFS,
		userDir:    userDir,
		projectDir: projectDir,
	}
}

// LoadRequired 加载一个必需片段:内置为底,用户、项目目录依次覆盖。
// 三层都拿不到非空内容则返回 error(对齐 Java loadRequired 的"缺资源即抛")。
func (r *Repository) LoadRequired(relativePath string) (string, error) {
	normalized, err := normalizeRel(relativePath)
	if err != nil {
		return "", err
	}

	content := r.loadBuiltin(normalized)
	if v, ok := r.override(r.userDir, normalized); ok {
		content = v
	}
	if v, ok := r.override(r.projectDir, normalized); ok {
		content = v
	}

	if strings.TrimSpace(content) == "" {
		return "", fmt.Errorf("prompt resource missing: %s", normalized)
	}
	return strings.TrimSpace(content), nil
}

// loadBuiltin 从 embed.FS 读内置片段;不存在返回空串(交由上层判定是否缺失)。
func (r *Repository) loadBuiltin(relativePath string) string {
	if r.builtinFS == nil {
		return ""
	}
	data, err := fs.ReadFile(r.builtinFS, path.Join(builtinPrefix, relativePath))
	if err != nil {
		return ""
	}
	return string(data)
}

// override 从覆盖目录读同名文件;目录为空、文件不存在、或越出根目录都视为
// "无覆盖"(返回 ok=false)。落在根目录内的常规文件才生效。
func (r *Repository) override(root, relativePath string) (string, bool) {
	if root == "" {
		return "", false
	}
	full := filepath.Join(root, filepath.FromSlash(relativePath))
	// 路径穿越兜底:normalizeRel 已挡 "..",这里再确认 full 仍在 root 内。
	rel, err := filepath.Rel(root, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		return "", false
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return "", false
	}
	return string(data), true
}

// normalizeRel 规整相对路径:统一斜杠,拒绝绝对路径与含 ".." 的路径
// (路径穿越防护,对齐 Java normalize)。
func normalizeRel(relativePath string) (string, error) {
	if strings.TrimSpace(relativePath) == "" {
		return "", fmt.Errorf("relative path is blank")
	}
	normalized := strings.ReplaceAll(relativePath, "\\", "/")
	if strings.HasPrefix(normalized, "/") || strings.Contains(normalized, "..") {
		return "", fmt.Errorf("invalid prompt path: %s", relativePath)
	}
	return normalized, nil
}
