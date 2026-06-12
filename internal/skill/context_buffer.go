package skill

import (
	"strings"
	"sync"
)

// maxSkills 是上下文缓冲同时保留的已加载技能数上限(LRU)。
const maxSkills = 3

// SkillContextBuffer 缓冲最近被 load_skill 加载的技能正文。
// load_skill 工具 Push 正文进来,agent 在下一轮 Chat 前 Drain 出来、
// 作为一条 user message 注入,使技能正文进入对话上下文。
type SkillContextBuffer struct {
	mu      sync.Mutex
	order   []string
	entries map[string]string
}

func NewSkillContextBuffer() *SkillContextBuffer {
	return &SkillContextBuffer{
		order:   make([]string, 0, maxSkills),
		entries: make(map[string]string),
	}
}

func (b *SkillContextBuffer) Push(skillName string, body string) {
	if b == nil || skillName == "" || body == "" {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	delete(b.entries, skillName)
	b.order = removeName(b.order, skillName)
	b.entries[skillName] = body
	b.order = append(b.order, skillName)

	for len(b.order) > maxSkills {
		oldest := b.order[0]
		b.order = b.order[1:]
		delete(b.entries, oldest)
	}
}

func (b *SkillContextBuffer) Drain() string {
	if b == nil {
		return ""
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.order) == 0 {
		return ""
	}

	var sb strings.Builder
	for _, name := range b.order {
		body := strings.TrimSpace(b.entries[name])
		sb.WriteString("## 已加载 Skill: ")
		sb.WriteString(name)
		sb.WriteByte('\n')
		sb.WriteString(body)
		sb.WriteString("\n\n")
	}
	sb.WriteString("---\n")

	b.order = b.order[:0]
	b.entries = make(map[string]string)
	return sb.String()
}

func (b *SkillContextBuffer) IsEmpty() bool {
	if b == nil {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.order) == 0
}

func (b *SkillContextBuffer) Size() int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.order)
}

func (b *SkillContextBuffer) Clear() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.order = b.order[:0]
	b.entries = make(map[string]string)
}

func removeName(names []string, target string) []string {
	result := names[:0]
	for _, name := range names {
		if name != target {
			result = append(result, name)
		}
	}
	return result
}
