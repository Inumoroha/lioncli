package skill

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// SkillStateStore 把技能的禁用列表持久化到 skills.json。
// file 为空时退化为"全部启用、不落盘"的无操作存储。
type SkillStateStore struct {
	file string
	mu   sync.Mutex
}

func NewSkillStateStore(file string) *SkillStateStore {
	return &SkillStateStore{file: file}
}

func (s *SkillStateStore) File() string {
	if s == nil {
		return ""
	}
	return s.file
}

func (s *SkillStateStore) Disabled() map[string]struct{} {
	if s == nil || s.file == "" {
		return map[string]struct{}{}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	content, err := os.ReadFile(s.file)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]struct{}{}
		}
		fmt.Fprintf(os.Stderr, "⚠ skills.json 读取失败，忽略禁用列表: %v\n", err)
		return map[string]struct{}{}
	}

	if len(content) == 0 {
		return map[string]struct{}{}
	}

	var payload struct {
		Disabled []string `json:"disabled"`
	}
	if err := json.Unmarshal(content, &payload); err != nil {
		fmt.Fprintf(os.Stderr, "⚠ skills.json 解析失败，忽略禁用列表: %v\n", err)
		return map[string]struct{}{}
	}

	result := make(map[string]struct{}, len(payload.Disabled))
	for _, name := range payload.Disabled {
		if name != "" {
			result[name] = struct{}{}
		}
	}
	return result
}

func (s *SkillStateStore) Disable(name string) {
	if s == nil || name == "" {
		return
	}

	disabled := s.Disabled()
	disabled[name] = struct{}{}
	s.write(disabled)
}

func (s *SkillStateStore) Enable(name string) {
	if s == nil || name == "" {
		return
	}

	disabled := s.Disabled()
	delete(disabled, name)
	s.write(disabled)
}

func (s *SkillStateStore) write(disabled map[string]struct{}) {
	if s == nil || s.file == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	names := make([]string, 0, len(disabled))
	for name := range disabled {
		names = append(names, name)
	}

	payload := struct {
		Disabled []string `json:"disabled"`
	}{
		Disabled: names,
	}

	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "⚠ skills.json 序列化失败: %v\n", err)
		return
	}

	if err := os.MkdirAll(filepath.Dir(s.file), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "⚠ skills.json 目录创建失败: %v\n", err)
		return
	}
	if err := os.WriteFile(s.file, data, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "⚠ skills.json 写入失败: %v\n", err)
	}
}
