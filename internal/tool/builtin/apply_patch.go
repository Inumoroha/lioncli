package builtin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"teacli/internal/lsp"
	"teacli/internal/policy"
	"teacli/internal/render"
	"teacli/internal/tool"
)

func init() {
	tool.RegisterAuto(newApplyPatchTool())
}

type patchFileOpKind int

const (
	patchAdd patchFileOpKind = iota
	patchUpdate
	patchDelete
)

type patchFileOp struct {
	kind      patchFileOpKind
	path      string
	moveTo    string
	oldLines  []string
	newLines  []string
	hasChange bool
}

func newApplyPatchTool() tool.Tool {
	return tool.Tool{
		Name:        "apply_patch",
		Description: "Apply a reviewable workspace patch using the *** Begin Patch format.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"patch": map[string]any{
					"type":        "string",
					"description": "Patch text beginning with *** Begin Patch and ending with *** End Patch.",
				},
			},
			"required": []string{"patch"},
		},
		Execute: func(_ context.Context, args map[string]any) (string, error) {
			patchText := tool.StringArg(args, "patch")
			if strings.TrimSpace(patchText) == "" {
				return "", fmt.Errorf("missing required parameter: patch")
			}
			return applyWorkspacePatch(patchText)
		},
	}
}

func applyWorkspacePatch(patchText string) (string, error) {
	ops, err := parsePatch(patchText)
	if err != nil {
		return "", err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to resolve working directory: %w", err)
	}
	guard, err := policy.NewPathGuard(cwd)
	if err != nil {
		return "", fmt.Errorf("failed to initialize path guard: %w", err)
	}

	type applied struct {
		path   string
		safe   string
		before *string
		after  *string
	}
	appliedOps := make([]applied, 0, len(ops))
	for _, op := range ops {
		safePath, err := guard.ResolveSafe(op.path)
		if err != nil {
			return "", err
		}
		var moveSafe string
		if op.moveTo != "" {
			moveSafe, err = guard.ResolveSafe(op.moveTo)
			if err != nil {
				return "", err
			}
		}

		before, after, err := applyPatchOp(op, safePath, moveSafe)
		if err != nil {
			return "", err
		}
		displayPath := op.path
		displaySafe := safePath
		if op.moveTo != "" {
			displayPath = op.moveTo
			displaySafe = moveSafe
		}
		appliedOps = append(appliedOps, applied{
			path:   displayPath,
			safe:   displaySafe,
			before: before,
			after:  after,
		})
	}

	var b strings.Builder
	fmt.Fprintf(&b, "applied patch to %d file(s)", len(appliedOps))
	for _, item := range appliedOps {
		fmt.Fprintf(&b, "\n\n%s", render.UnifiedDiff(item.path, item.before, item.after))
		if item.after != nil {
			if report := lsp.NewManager().RunPostEditHook(item.path, item.safe); !report.IsEmpty() {
				fmt.Fprintf(&b, "\n\n%s", report.PromptText)
			}
		}
	}
	return b.String(), nil
}

func applyPatchOp(op patchFileOp, safePath, moveSafePath string) (*string, *string, error) {
	switch op.kind {
	case patchAdd:
		if _, err := os.Stat(safePath); err == nil {
			return nil, nil, fmt.Errorf("cannot add %s: file already exists", op.path)
		} else if !os.IsNotExist(err) {
			return nil, nil, fmt.Errorf("cannot inspect %s: %w", op.path, err)
		}
		after := joinPatchLines(op.newLines)
		if err := os.MkdirAll(filepath.Dir(safePath), 0o755); err != nil {
			return nil, nil, fmt.Errorf("failed to create parent directory: %w", err)
		}
		if err := os.WriteFile(safePath, []byte(after), 0o644); err != nil {
			return nil, nil, fmt.Errorf("failed to add file: %w", err)
		}
		return nil, &after, nil

	case patchUpdate:
		raw, err := os.ReadFile(safePath)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read file for update: %w", err)
		}
		before := string(raw)
		after, err := applyLineReplacement(before, op.oldLines, op.newLines, op.hasChange)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to update %s: %w", op.path, err)
		}
		target := safePath
		if moveSafePath != "" {
			target = moveSafePath
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return nil, nil, fmt.Errorf("failed to create target parent directory: %w", err)
			}
		}
		if err := os.WriteFile(target, []byte(after), 0o644); err != nil {
			return nil, nil, fmt.Errorf("failed to write updated file: %w", err)
		}
		if moveSafePath != "" && moveSafePath != safePath {
			if err := os.Remove(safePath); err != nil {
				return nil, nil, fmt.Errorf("failed to remove moved source: %w", err)
			}
		}
		return &before, &after, nil

	case patchDelete:
		raw, err := os.ReadFile(safePath)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read file for delete: %w", err)
		}
		before := string(raw)
		if err := os.Remove(safePath); err != nil {
			return nil, nil, fmt.Errorf("failed to delete file: %w", err)
		}
		return &before, nil, nil

	default:
		return nil, nil, fmt.Errorf("unknown patch operation")
	}
}

func parsePatch(patchText string) ([]patchFileOp, error) {
	lines := strings.Split(strings.ReplaceAll(patchText, "\r\n", "\n"), "\n")
	if len(lines) < 2 || strings.TrimSpace(lines[0]) != "*** Begin Patch" {
		return nil, fmt.Errorf("patch must start with *** Begin Patch")
	}
	if strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) < 2 || strings.TrimSpace(lines[len(lines)-1]) != "*** End Patch" {
		return nil, fmt.Errorf("patch must end with *** End Patch")
	}

	var ops []patchFileOp
	for i := 1; i < len(lines)-1; {
		line := lines[i]
		switch {
		case strings.HasPrefix(line, "*** Add File: "):
			path := strings.TrimSpace(strings.TrimPrefix(line, "*** Add File: "))
			i++
			var newLines []string
			for i < len(lines)-1 && !strings.HasPrefix(lines[i], "*** ") {
				if !strings.HasPrefix(lines[i], "+") {
					return nil, fmt.Errorf("add file %s contains non-add line: %q", path, lines[i])
				}
				newLines = append(newLines, strings.TrimPrefix(lines[i], "+"))
				i++
			}
			ops = append(ops, patchFileOp{kind: patchAdd, path: path, newLines: newLines})

		case strings.HasPrefix(line, "*** Delete File: "):
			path := strings.TrimSpace(strings.TrimPrefix(line, "*** Delete File: "))
			ops = append(ops, patchFileOp{kind: patchDelete, path: path})
			i++

		case strings.HasPrefix(line, "*** Update File: "):
			path := strings.TrimSpace(strings.TrimPrefix(line, "*** Update File: "))
			op := patchFileOp{kind: patchUpdate, path: path}
			i++
			if i < len(lines)-1 && strings.HasPrefix(lines[i], "*** Move to: ") {
				op.moveTo = strings.TrimSpace(strings.TrimPrefix(lines[i], "*** Move to: "))
				i++
			}
			for i < len(lines)-1 && !strings.HasPrefix(lines[i], "*** ") {
				if lines[i] == "@@" || strings.HasPrefix(lines[i], "@@ ") {
					i++
					continue
				}
				if lines[i] == "*** End of File" {
					i++
					break
				}
				if len(lines[i]) == 0 {
					return nil, fmt.Errorf("update file %s contains empty patch line without prefix", path)
				}
				prefix := lines[i][0]
				text := lines[i][1:]
				switch prefix {
				case ' ':
					op.oldLines = append(op.oldLines, text)
					op.newLines = append(op.newLines, text)
					op.hasChange = true
				case '-':
					op.oldLines = append(op.oldLines, text)
					op.hasChange = true
				case '+':
					op.newLines = append(op.newLines, text)
					op.hasChange = true
				default:
					return nil, fmt.Errorf("update file %s contains invalid patch line: %q", path, lines[i])
				}
				i++
			}
			if op.moveTo == "" && !op.hasChange {
				return nil, fmt.Errorf("update file %s has no changes", path)
			}
			ops = append(ops, op)

		default:
			return nil, fmt.Errorf("unexpected patch line: %q", line)
		}
	}
	if len(ops) == 0 {
		return nil, fmt.Errorf("patch contains no file operations")
	}
	for _, op := range ops {
		if strings.TrimSpace(op.path) == "" {
			return nil, fmt.Errorf("patch file path cannot be empty")
		}
		if strings.TrimSpace(op.moveTo) == "" && op.moveTo != "" {
			return nil, fmt.Errorf("patch move target cannot be empty")
		}
	}
	return ops, nil
}

func applyLineReplacement(before string, oldLines, newLines []string, hasChange bool) (string, error) {
	if !hasChange {
		return before, nil
	}
	oldText := joinPatchLines(oldLines)
	newText := joinPatchLines(newLines)
	idx := strings.Index(before, oldText)
	if idx < 0 {
		return "", fmt.Errorf("patch context not found")
	}
	if strings.Index(before[idx+len(oldText):], oldText) >= 0 {
		return "", fmt.Errorf("patch context is ambiguous")
	}
	return before[:idx] + newText + before[idx+len(oldText):], nil
}

func joinPatchLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}
