package render

import (
	"strings"
	"testing"
)

func TestUnifiedDiffChangedFile(t *testing.T) {
	before := "a\nb\nc\n"
	after := "a\nB\nc\n"

	got := UnifiedDiff("file.txt", &before, &after)
	for _, want := range []string{
		"--- file.txt",
		"+++ file.txt",
		"@@ -1,3 +1,3 @@",
		"-b",
		"+B",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("diff missing %q:\n%s", want, got)
		}
	}
}

func TestUnifiedDiffNewAndDeletedFiles(t *testing.T) {
	after := "one\ntwo\n"
	if got := UnifiedDiff("new.txt", nil, &after); !strings.Contains(got, "@@ -0,0 +1,2 @@") || !strings.Contains(got, "+one") {
		t.Fatalf("unexpected new-file diff:\n%s", got)
	}

	before := "one\ntwo\n"
	if got := UnifiedDiff("old.txt", &before, nil); !strings.Contains(got, "@@ -1,2 +0,0 @@") || !strings.Contains(got, "-one") {
		t.Fatalf("unexpected delete-file diff:\n%s", got)
	}
}

func TestUnifiedDiffEmptyToContentUsesZeroStart(t *testing.T) {
	before := ""
	after := "one\n"

	got := UnifiedDiff("file.txt", &before, &after)
	if !strings.Contains(got, "@@ -0,0 +1,1 @@") || !strings.Contains(got, "+one") {
		t.Fatalf("unexpected empty-to-content diff:\n%s", got)
	}
}

func TestUnifiedDiffLargeFileSummarizes(t *testing.T) {
	before := strings.Repeat("a\n", maxDiffLines+1)
	after := strings.Repeat("b\n", maxDiffLines+1)

	got := UnifiedDiff("large.txt", &before, &after)
	if !strings.Contains(got, "diff too large") || strings.Contains(got, "-a") || strings.Contains(got, "+b") {
		t.Fatalf("unexpected large diff summary:\n%s", got)
	}
}

func TestUnifiedDiffNewFilePreviewTruncates(t *testing.T) {
	after := strings.Repeat("line\n", maxPreviewLines+1)

	got := UnifiedDiff("new.txt", nil, &after)
	if !strings.Contains(got, "diff truncated, 1 more line(s)") {
		t.Fatalf("expected truncated preview:\n%s", got)
	}
}
