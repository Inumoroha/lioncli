package policy

import (
	"path/filepath"
	"testing"
)

func TestPathGuardResolveSafeAllowsProjectPaths(t *testing.T) {
	root := t.TempDir()
	guard := MustPathGuard(root)

	got, err := guard.ResolveSafe(filepath.Join("internal", "file.txt"))
	if err != nil {
		t.Fatalf("ResolveSafe returned error: %v", err)
	}
	want := filepath.Join(root, "internal", "file.txt")
	if got != want {
		t.Fatalf("ResolveSafe = %q, want %q", got, want)
	}
}

func TestPathGuardResolveSafeRejectsEscapes(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	guard := MustPathGuard(root)

	if _, err := guard.ResolveSafe(outside); err == nil {
		t.Fatal("expected absolute path outside root to be rejected")
	}
	if _, err := guard.ResolveSafe(filepath.Join("..", "outside.txt")); err == nil {
		t.Fatal("expected parent traversal to be rejected")
	}
}
