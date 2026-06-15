package policy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type PathGuard struct {
	root string
}

func NewPathGuard(root string) (*PathGuard, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("project root cannot be empty")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	real := filepath.Clean(abs)
	if resolved, err := filepath.EvalSymlinks(real); err == nil {
		real = resolved
	}
	return &PathGuard{root: filepath.Clean(real)}, nil
}

func MustPathGuard(root string) *PathGuard {
	guard, err := NewPathGuard(root)
	if err != nil {
		panic(err)
	}
	return guard
}

func (g *PathGuard) Root() string {
	return g.root
}

func (g *PathGuard) ResolveSafe(input string) (string, error) {
	if strings.TrimSpace(input) == "" {
		return "", newError("path cannot be empty")
	}

	var target string
	if filepath.IsAbs(input) {
		target = filepath.Clean(input)
	} else {
		target = filepath.Clean(filepath.Join(g.root, input))
	}
	realTarget := resolveRealPath(target)

	if !inside(g.root, realTarget) {
		return "", newError(fmt.Sprintf("path escapes project root: %s is not inside %s", input, g.root))
	}
	return realTarget, nil
}

func resolveRealPath(target string) string {
	existing := target
	for {
		if _, err := os.Stat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return filepath.Clean(target)
		}
		existing = parent
	}
	realExisting, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return filepath.Clean(target)
	}
	rel, err := filepath.Rel(existing, target)
	if err != nil {
		return filepath.Clean(target)
	}
	return filepath.Clean(filepath.Join(realExisting, rel))
}

func inside(root string, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
