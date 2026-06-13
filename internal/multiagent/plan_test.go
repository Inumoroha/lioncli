package multiagent

import "testing"

func TestParsePlan_CleanJSON(t *testing.T) {
	raw := `{"steps":[{"id":1,"description":"调研","dependencies":[]},{"id":2,"description":"实现","dependencies":[1]}]}`
	steps := parsePlan(raw, "做点什么")
	if len(steps) != 2 {
		t.Fatalf("want 2 steps, got %d", len(steps))
	}
	if steps[1].ID != 2 || len(steps[1].Dependencies) != 1 || steps[1].Dependencies[0] != 1 {
		t.Fatalf("step 2 deps wrong: %+v", steps[1])
	}
}

func TestParsePlan_FencedAndProse(t *testing.T) {
	raw := "好的,这是计划:\n```json\n{\"steps\":[{\"id\":1,\"description\":\"唯一步骤\"}]}\n```\n以上。"
	steps := parsePlan(raw, "goal")
	if len(steps) != 1 || steps[0].Description != "唯一步骤" {
		t.Fatalf("fenced parse failed: %+v", steps)
	}
}

func TestParsePlan_FallbackSingleStep(t *testing.T) {
	steps := parsePlan("完全不是 JSON 的胡言乱语", "我的目标")
	if len(steps) != 1 || steps[0].Description != "我的目标" {
		t.Fatalf("fallback failed: %+v", steps)
	}
}

func TestNormalizeSteps_DropsBadDepsAndEmpty(t *testing.T) {
	in := []Step{
		{ID: 1, Description: "ok"},
		{ID: 2, Description: "  "},          // 空描述,应丢弃
		{ID: 3, Description: "dep", Dependencies: []int{1, 99, 3}}, // 99 不存在、3 自指,均剔除
	}
	out := normalizeSteps(in)
	if len(out) != 2 {
		t.Fatalf("want 2 steps after drop, got %d", len(out))
	}
	last := out[1]
	if len(last.Dependencies) != 1 || last.Dependencies[0] != 1 {
		t.Fatalf("bad deps not cleaned: %+v", last.Dependencies)
	}
}

func TestTopoOrder_RespectsDependencies(t *testing.T) {
	steps := []Step{
		{ID: 1, Description: "a", Dependencies: []int{2}},
		{ID: 2, Description: "b"},
	}
	ordered := topoOrder(steps)
	if ordered[0].ID != 2 || ordered[1].ID != 1 {
		t.Fatalf("topo order wrong: %d then %d", ordered[0].ID, ordered[1].ID)
	}
}

func TestTopoOrder_CycleFallsBackToInputOrder(t *testing.T) {
	steps := []Step{
		{ID: 1, Description: "a", Dependencies: []int{2}},
		{ID: 2, Description: "b", Dependencies: []int{1}},
	}
	ordered := topoOrder(steps)
	if len(ordered) != 2 {
		t.Fatalf("cycle should still return all steps, got %d", len(ordered))
	}
}
