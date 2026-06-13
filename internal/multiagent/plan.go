package multiagent

import (
	"encoding/json"
	"fmt"
	"strings"
)

// maxSteps 限制计划规模,避免规划者产出过长导致协作失控。
const maxSteps = 12

// Step 是计划里的一个子任务。
type Step struct {
	ID           int    `json:"id"`
	Description  string `json:"description"`
	Dependencies []int  `json:"dependencies"`
}

type planEnvelope struct {
	Steps []Step `json:"steps"`
}

// parsePlan 从规划者的自然语言输出里抽取 JSON 计划。
// 容错策略:剥掉可能的 markdown 围栏、截取最外层 {...} 再解析;任何失败都降级为
// "整个目标作为单一步骤",保证编排流程始终能推进,而不是因解析失败直接报错。
func parsePlan(raw, goal string) []Step {
	if steps, ok := tryParseSteps(raw); ok {
		return normalizeSteps(steps)
	}
	if jsonPart := extractJSONObject(raw); jsonPart != "" {
		if steps, ok := tryParseSteps(jsonPart); ok {
			return normalizeSteps(steps)
		}
	}
	// 兜底:单步计划。
	return []Step{{ID: 1, Description: strings.TrimSpace(goal), Dependencies: nil}}
}

func tryParseSteps(s string) ([]Step, bool) {
	var env planEnvelope
	if err := json.Unmarshal([]byte(strings.TrimSpace(s)), &env); err != nil {
		return nil, false
	}
	if len(env.Steps) == 0 {
		return nil, false
	}
	return env.Steps, true
}

// extractJSONObject 返回字符串中第一个 '{' 到最后一个 '}' 之间的子串(含两端)。
func extractJSONObject(s string) string {
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start < 0 || end <= start {
		return ""
	}
	return s[start : end+1]
}

// normalizeSteps 清洗规划者产出:丢弃空描述、补齐缺失 id、裁剪超量步骤,
// 并剔除指向不存在步骤的依赖,保证后续拓扑排序不会因脏数据卡死。
func normalizeSteps(in []Step) []Step {
	out := make([]Step, 0, len(in))
	for i, s := range in {
		if strings.TrimSpace(s.Description) == "" {
			continue
		}
		if s.ID <= 0 {
			s.ID = i + 1
		}
		s.Description = strings.TrimSpace(s.Description)
		out = append(out, s)
		if len(out) >= maxSteps {
			break
		}
	}
	if len(out) == 0 {
		return out
	}

	valid := make(map[int]struct{}, len(out))
	for _, s := range out {
		valid[s.ID] = struct{}{}
	}
	for i := range out {
		deps := out[i].Dependencies[:0]
		for _, d := range out[i].Dependencies {
			if _, ok := valid[d]; ok && d != out[i].ID {
				deps = append(deps, d)
			}
		}
		out[i].Dependencies = deps
	}
	return out
}

// topoOrder 用 Kahn 算法给步骤做拓扑排序;若检测到环,则按原始顺序兜底返回
// (宁可顺序不完美也要能跑完,而不是直接失败)。
func topoOrder(steps []Step) []Step {
	byID := make(map[int]Step, len(steps))
	indeg := make(map[int]int, len(steps))
	order := make([]int, 0, len(steps)) // 保持稳定输出顺序
	for _, s := range steps {
		byID[s.ID] = s
		indeg[s.ID] = 0
		order = append(order, s.ID)
	}
	for _, s := range steps {
		for range s.Dependencies {
			indeg[s.ID]++
		}
	}

	queue := make([]int, 0, len(steps))
	for _, id := range order {
		if indeg[id] == 0 {
			queue = append(queue, id)
		}
	}

	result := make([]Step, 0, len(steps))
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		result = append(result, byID[cur])
		// 释放依赖 cur 的后继。
		for _, id := range order {
			s := byID[id]
			for _, d := range s.Dependencies {
				if d == cur {
					indeg[id]--
					if indeg[id] == 0 {
						queue = append(queue, id)
					}
				}
			}
		}
	}

	if len(result) != len(steps) { // 有环:兜底原序
		return steps
	}
	return result
}

// renderPlan 把计划格式化成给用户看的清单。
func renderPlan(steps []Step) string {
	var b strings.Builder
	b.WriteString("📋 执行计划:\n")
	for _, s := range steps {
		if len(s.Dependencies) > 0 {
			b.WriteString(fmt.Sprintf("  %d. %s (依赖: %v)\n", s.ID, s.Description, s.Dependencies))
		} else {
			b.WriteString(fmt.Sprintf("  %d. %s\n", s.ID, s.Description))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
