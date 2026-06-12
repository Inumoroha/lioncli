package memory

import (
	"sync"
	"testing"
	"time"
)

// TestRandomSuffixUniqueConcurrent 验证 ID 后缀在高并发快速调用下也唯一,
// 守护 randomSuffix 从纳秒戳改为原子计数器的修复(旧实现会因纳秒相同而碰撞)。
func TestRandomSuffixUniqueConcurrent(t *testing.T) {
	t.Parallel()

	const goroutines = 16
	const perG = 500

	var wg sync.WaitGroup
	var mu sync.Mutex
	seen := make(map[string]struct{}, goroutines*perG)
	dup := 0

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			local := make([]string, 0, perG)
			for j := 0; j < perG; j++ {
				local = append(local, randomSuffix())
			}
			mu.Lock()
			for _, s := range local {
				if _, ok := seen[s]; ok {
					dup++
				}
				seen[s] = struct{}{}
			}
			mu.Unlock()
		}()
	}
	wg.Wait()

	if dup != 0 {
		t.Fatalf("randomSuffix 产生了 %d 个重复 ID,应为 0", dup)
	}
	if len(seen) != goroutines*perG {
		t.Fatalf("唯一 ID 数 = %d, 期望 %d", len(seen), goroutines*perG)
	}
}

// TestTimeDecayFactorMonotonic 验证时间衰减是严格单调递减且夹在 [floor, 1],
// 守护从"线性+12h 后全塌成 0.5"改为指数半衰减的修复。
func TestTimeDecayFactorMonotonic(t *testing.T) {
	t.Parallel()

	now := time.Now()
	// 关键回归点:旧实现下 13h 和 30h 都是 0.5(无区分度)。新实现必须严格区分。
	f13 := timeDecayFactor(now.Add(-13 * time.Hour))
	f30 := timeDecayFactor(now.Add(-30 * time.Hour))
	if !(f13 > f30) {
		t.Fatalf("13h 衰减 %.4f 应严格大于 30h 衰减 %.4f(旧 bug:两者都=0.5)", f13, f30)
	}

	// 单调性:一串递增的 age,衰减应单调不增。
	prev := timeDecayFactor(now)
	for _, h := range []float64{1, 6, 24, 72, 168, 336, 1000} {
		cur := timeDecayFactor(now.Add(-time.Duration(h) * time.Hour))
		if cur > prev {
			t.Fatalf("age=%.0fh 衰减 %.4f 不应大于更新条目的 %.4f", h, cur, prev)
		}
		if cur < timeDecayFloor-1e-9 || cur > 1.0+1e-9 {
			t.Fatalf("age=%.0fh 衰减 %.4f 越界 [%.2f, 1]", h, cur, timeDecayFloor)
		}
		prev = cur
	}

	// 半衰期处应约等于 0.5。
	if got := timeDecayFactor(now.Add(-time.Duration(timeDecayHalfLifeHours) * time.Hour)); got < 0.49 || got > 0.51 {
		t.Fatalf("半衰期处衰减 = %.4f, 期望约 0.5", got)
	}
}

// TestEvictedEntriesBounded 验证被驱逐条目列表有上限,不会无限增长。
func TestEvictedEntriesBounded(t *testing.T) {
	t.Parallel()

	// maxTokens=1 + 每条 1 token,确保每次 Store 都驱逐上一条。
	mem := NewConversationMemory(1)
	total := maxEvictedRetained + 100
	for i := 0; i < total; i++ {
		mem.Store(NewMemoryEntry("e-"+randomSuffix(), "x", MemoryTypeConversation, nil, 1))
	}
	if got := len(mem.EvictedEntries()); got > maxEvictedRetained {
		t.Fatalf("EvictedEntries 长度 %d 超过上限 %d", got, maxEvictedRetained)
	}
}

// TestLongTermMemoryCapEvictsOldest 验证长期记忆超容量时按 FIFO 淘汰最旧条目。
func TestLongTermMemoryCapEvictsOldest(t *testing.T) {
	t.Parallel()

	mem, err := NewLongTermMemory(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxLongTermEntries+10; i++ {
		mem.Store(NewMemoryEntry("k-"+randomSuffix(), "v", MemoryTypeFact, nil, 1))
	}
	if got := mem.Size(); got != maxLongTermEntries {
		t.Fatalf("长期记忆条数 = %d, 期望被夹到上限 %d", got, maxLongTermEntries)
	}
}
