package render

import (
	"fmt"
	"strings"
)

const (
	diffContextLines = 2
	maxDiffLines     = 2000
	maxPreviewLines  = 200
)

type DiffOpType int

const (
	DiffEqual DiffOpType = iota
	DiffAdd
	DiffDelete
)

type DiffOp struct {
	Type        DiffOpType
	Text        string
	BeforeIndex int
	AfterIndex  int
}

type Hunk struct {
	BeforeStart int
	BeforeCount int
	AfterStart  int
	AfterCount  int
	Ops         []DiffOp
}

func UnifiedDiff(filePath string, before, after *string) string {
	if strings.TrimSpace(filePath) == "" {
		filePath = "(unnamed)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "--- %s\n", filePath)
	fmt.Fprintf(&b, "+++ %s\n", filePath)

	switch {
	case before == nil && after == nil:
		b.WriteString("(empty diff)\n")
	case before == nil:
		writeNewFileDiff(&b, *after)
	case after == nil:
		writeDeleteFileDiff(&b, *before)
	case *before == *after:
		b.WriteString("(content unchanged)\n")
	default:
		beforeLines := splitLines(*before)
		afterLines := splitLines(*after)
		if tooLargeForInlineDiff(beforeLines, afterLines) {
			writeLargeDiffSummary(&b, beforeLines, afterLines)
			break
		}
		for _, hunk := range GroupHunks(ComputeDiff(beforeLines, afterLines)) {
			writeHunk(&b, hunk)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func ComputeDiff(before, after []string) []DiffOp {
	n, m := len(before), len(after)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if before[i] == after[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	var ops []DiffOp
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case before[i] == after[j]:
			ops = append(ops, DiffOp{Type: DiffEqual, Text: before[i], BeforeIndex: i, AfterIndex: j})
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			ops = append(ops, DiffOp{Type: DiffDelete, Text: before[i], BeforeIndex: i, AfterIndex: -1})
			i++
		default:
			ops = append(ops, DiffOp{Type: DiffAdd, Text: after[j], BeforeIndex: -1, AfterIndex: j})
			j++
		}
	}
	for i < n {
		ops = append(ops, DiffOp{Type: DiffDelete, Text: before[i], BeforeIndex: i, AfterIndex: -1})
		i++
	}
	for j < m {
		ops = append(ops, DiffOp{Type: DiffAdd, Text: after[j], BeforeIndex: -1, AfterIndex: j})
		j++
	}
	return ops
}

func GroupHunks(ops []DiffOp) []Hunk {
	var hunks []Hunk
	idx := 0
	for idx < len(ops) {
		for idx < len(ops) && ops[idx].Type == DiffEqual {
			idx++
		}
		if idx >= len(ops) {
			break
		}

		hunkStart := max(0, idx-diffContextLines)
		hunkEnd := idx
		equalRun := 0
		for hunkEnd < len(ops) {
			if ops[hunkEnd].Type == DiffEqual {
				equalRun++
				if equalRun >= 2*diffContextLines {
					break
				}
			} else {
				equalRun = 0
			}
			hunkEnd++
		}
		hunkClose := min(len(ops), hunkEnd+diffContextLines-equalRun)
		hunkOps := append([]DiffOp(nil), ops[hunkStart:hunkClose]...)
		hunks = append(hunks, Hunk{
			BeforeStart: firstBeforeIndex(hunkOps),
			BeforeCount: countBefore(hunkOps),
			AfterStart:  firstAfterIndex(hunkOps),
			AfterCount:  countAfter(hunkOps),
			Ops:         hunkOps,
		})
		idx = hunkClose
	}
	return hunks
}

func writeNewFileDiff(b *strings.Builder, after string) {
	lines := splitLines(after)
	fmt.Fprintf(b, "@@ -0,0 +1,%d @@\n", len(lines))
	for i, line := range previewLines(lines) {
		fmt.Fprintf(b, "+%s\n", line)
		if i == maxPreviewLines-1 && len(lines) > maxPreviewLines {
			fmt.Fprintf(b, "... diff truncated, %d more line(s)\n", len(lines)-maxPreviewLines)
			break
		}
	}
}

func writeDeleteFileDiff(b *strings.Builder, before string) {
	lines := splitLines(before)
	fmt.Fprintf(b, "@@ -1,%d +0,0 @@\n", len(lines))
	for i, line := range previewLines(lines) {
		fmt.Fprintf(b, "-%s\n", line)
		if i == maxPreviewLines-1 && len(lines) > maxPreviewLines {
			fmt.Fprintf(b, "... diff truncated, %d more line(s)\n", len(lines)-maxPreviewLines)
			break
		}
	}
}

func tooLargeForInlineDiff(before, after []string) bool {
	return len(before) > maxDiffLines ||
		len(after) > maxDiffLines ||
		len(before)*len(after) > maxDiffLines*maxDiffLines
}

func writeLargeDiffSummary(b *strings.Builder, before, after []string) {
	fmt.Fprintf(b, "(diff too large: %d -> %d lines; showing no inline hunks)\n", len(before), len(after))
}

func previewLines(lines []string) []string {
	if len(lines) <= maxPreviewLines {
		return lines
	}
	return lines[:maxPreviewLines]
}

func writeHunk(b *strings.Builder, h Hunk) {
	fmt.Fprintf(b, "@@ -%d,%d +%d,%d @@\n", hunkLineStart(h.BeforeStart, h.BeforeCount), h.BeforeCount, hunkLineStart(h.AfterStart, h.AfterCount), h.AfterCount)
	for _, op := range h.Ops {
		switch op.Type {
		case DiffEqual:
			fmt.Fprintf(b, " %s\n", op.Text)
		case DiffAdd:
			fmt.Fprintf(b, "+%s\n", op.Text)
		case DiffDelete:
			fmt.Fprintf(b, "-%s\n", op.Text)
		}
	}
}

func hunkLineStart(index, count int) int {
	if count == 0 {
		return 0
	}
	return index + 1
}

func splitLines(text string) []string {
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func firstBeforeIndex(ops []DiffOp) int {
	for _, op := range ops {
		if op.BeforeIndex >= 0 {
			return op.BeforeIndex
		}
	}
	return 0
}

func firstAfterIndex(ops []DiffOp) int {
	for _, op := range ops {
		if op.AfterIndex >= 0 {
			return op.AfterIndex
		}
	}
	return 0
}

func countBefore(ops []DiffOp) int {
	count := 0
	for _, op := range ops {
		if op.Type != DiffAdd {
			count++
		}
	}
	return count
}

func countAfter(ops []DiffOp) int {
	count := 0
	for _, op := range ops {
		if op.Type != DiffDelete {
			count++
		}
	}
	return count
}
