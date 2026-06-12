package memory

import (
	"math"
	"sort"
	"strings"
	"time"
)

// timeDecayHalfLifeHours 是时间衰减的半衰期:每过这么多小时,相关性权重减半。
// 7 天让"今天 vs 昨天"有可感的区分度,又不会让一周前的记忆迅速归零。
const timeDecayHalfLifeHours = 168.0

// timeDecayFloor 是衰减因子的下限:再旧的记忆也保留这点可检索权重,
// 否则长期记忆会被时间彻底淹没,失去"长期"的意义。
const timeDecayFloor = 0.3

type MemoryRetriever struct {
	shortTerm *ConversationMemory
	longTerm  *LongTermMemory
}

func NewMemoryRetriever(shortTerm *ConversationMemory, longTerm *LongTermMemory) *MemoryRetriever {
	return &MemoryRetriever{shortTerm: shortTerm, longTerm: longTerm}
}

func (r *MemoryRetriever) Retrieve(query string, limit int) []MemoryEntry {
	var scored []scoredEntry
	for _, entry := range r.shortTerm.GetAll() {
		score := computeRelevanceScore(entry, query)
		if score > 0 {
			scored = append(scored, scoredEntry{entry: entry, score: score, fromShortTerm: true})
		}
	}
	for _, entry := range r.longTerm.GetAll() {
		score := computeRelevanceScore(entry, query)
		if score > 0 {
			scored = append(scored, scoredEntry{entry: entry, score: score * 1.2, fromShortTerm: false})
		}
	}

	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})
	if limit > 0 && len(scored) > limit {
		scored = scored[:limit]
	}
	results := make([]MemoryEntry, 0, len(scored))
	for _, item := range scored {
		results = append(results, item.entry)
	}
	return results
}

func (r *MemoryRetriever) RetrieveLongTerm(query string, limit int) []MemoryEntry {
	var scored []scoredEntry
	for _, entry := range r.longTerm.GetAll() {
		score := computeRelevanceScore(entry, query) * 1.2
		if score > 0 {
			scored = append(scored, scoredEntry{entry: entry, score: score})
		}
	}
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})
	if limit > 0 && len(scored) > limit {
		scored = scored[:limit]
	}
	results := make([]MemoryEntry, 0, len(scored))
	for _, item := range scored {
		results = append(results, item.entry)
	}
	return results
}

func (r *MemoryRetriever) BuildContextForQuery(query string, maxTokens int) string {
	relevant := r.RetrieveLongTerm(query, 10)
	if len(relevant) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("## Relevant long-term memory\n\n")
	usedTokens := 0
	for _, entry := range relevant {
		if usedTokens+entry.TokenCount > maxTokens {
			break
		}
		builder.WriteString("- [")
		builder.WriteString(string(entry.Type))
		builder.WriteString("] ")
		builder.WriteString(entry.Content)
		builder.WriteString("\n")
		usedTokens += entry.TokenCount
	}
	builder.WriteString("\n")
	return builder.String()
}

type scoredEntry struct {
	entry         MemoryEntry
	score         float64
	fromShortTerm bool
}

func computeRelevanceScore(entry MemoryEntry, query string) float64 {
	contentLower := strings.ToLower(entry.Content)
	queryLower := strings.ToLower(query)
	if queryLower != "" && strings.Contains(contentLower, queryLower) {
		return 1.0
	}

	queryWords := TokenizeMemoryQuery(queryLower)
	matched := 0
	for _, word := range queryWords {
		if word != "" && strings.Contains(contentLower, word) {
			matched++
		}
	}
	if matched == 0 || len(queryWords) == 0 {
		return 0
	}

	keywordScore := float64(matched) / float64(len(queryWords))
	return keywordScore * timeDecayFactor(entry.Timestamp)
}

// timeDecayFactor 按半衰期做指数衰减:factor = 0.5^(age/halfLife),夹到 [floor, 1]。
// 比旧的线性公式更平滑——任意两个时间点都有区分度,而非 12 小时后一律塌成同一个值。
func timeDecayFactor(timestamp time.Time) float64 {
	ageHours := time.Since(timestamp).Hours()
	if ageHours <= 0 {
		return 1.0
	}
	factor := math.Pow(0.5, ageHours/timeDecayHalfLifeHours)
	if factor < timeDecayFloor {
		return timeDecayFloor
	}
	return factor
}
