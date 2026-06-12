package rag

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestCollectIndexFilesSkipsHiddenAndUnsupportedFiles(t *testing.T) {
	root := t.TempDir()

	mustWriteFile := func(rel, content string) {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll failed for %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile failed for %s: %v", path, err)
		}
	}

	mustWriteFile("main.go", "package main")
	mustWriteFile("README.md", "doc")
	mustWriteFile("assets/logo.png", "png")
	mustWriteFile(".git/config", "ignored")
	mustWriteFile(".hidden/secret.go", "package hidden")
	mustWriteFile("vendor/lib.go", "package vendor")
	mustWriteFile("nested/config.yaml", "name: test")

	files, err := collectIndexFiles(root)
	if err != nil {
		t.Fatalf("collectIndexFiles returned error: %v", err)
	}

	relativize := func(values []string) []string {
		result := make([]string, 0, len(values))
		for _, value := range values {
			rel, err := filepath.Rel(root, value)
			if err != nil {
				t.Fatalf("filepath.Rel failed: %v", err)
			}
			result = append(result, rel)
		}
		return result
	}

	relativeFiles := relativize(files)
	expectedIncluded := []string{"main.go", "README.md", filepath.Join("nested", "config.yaml")}
	for _, path := range expectedIncluded {
		if !slices.Contains(relativeFiles, path) {
			t.Fatalf("expected %q in %v", path, relativeFiles)
		}
	}

	expectedExcluded := []string{
		filepath.Join("assets", "logo.png"),
		filepath.Join(".git", "config"),
		filepath.Join(".hidden", "secret.go"),
		filepath.Join("vendor", "lib.go"),
	}
	for _, path := range expectedExcluded {
		if slices.Contains(relativeFiles, path) {
			t.Fatalf("did not expect %q in %v", path, relativeFiles)
		}
	}
}
