package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/glamour"

	"lioncli/internal/hitl"
	"lioncli/internal/render"
)

const (
	maxToolArgChars     = 500
	maxToolOutputLines  = 120
	maxToolOutputChars  = 12000
	toolOutputHeadLines = 80
	toolOutputTailLines = 30
)

type blockKind int

const (
	blockUser blockKind = iota
	blockAssistant
	blockTool
	blockError
	blockSystem
	blockApproval
)

type renderedBlock struct {
	kind     blockKind
	toolID   string
	toolName string
	toolArgs map[string]any
	body     string
	toolDone bool
	isError  bool

	approval *hitl.ApprovalRequest
}

func renderBlocks(blocks []renderedBlock, md *glamour.TermRenderer) string {
	if len(blocks) == 0 {
		return ""
	}
	parts := make([]string, 0, len(blocks))
	for _, b := range blocks {
		parts = append(parts, renderOne(b, md))
	}
	return strings.Join(parts, "\n\n")
}

func renderOne(b renderedBlock, md *glamour.TermRenderer) string {
	switch b.kind {
	case blockUser:
		return userBarStyle.Render("> You") + "\n" + bodyStyle.Render(b.body)
	case blockAssistant:
		return assistantBarStyle.Render("> Assistant") + "\n" + renderMarkdown(b.body, md)
	case blockTool:
		return renderToolBlock(b)
	case blockError:
		return errBarStyle.Render("! Error") + "\n" + bodyStyle.Render(b.body)
	case blockSystem:
		return systemBarStyle.Render("* System") + "\n" + bodyStyle.Render(b.body)
	case blockApproval:
		return renderApprovalBlock(b)
	default:
		return ""
	}
}

func renderApprovalBlock(b renderedBlock) string {
	if b.approval == nil {
		return ""
	}
	req := *b.approval
	header := approvalBarStyle.Render("! Approval required: " + req.ToolName)

	lines := []string{header}
	if server := hitl.MCPServerName(req.ToolName); server != "" {
		lines = append(lines, approvalMetaStyle.Render("MCP server: "+server))
	}
	lines = append(lines, approvalMetaStyle.Render("Level: "+req.DangerLevel))
	lines = append(lines, approvalMetaStyle.Render("Risk: "+req.RiskDescription))
	if req.CallerContext != "" {
		lines = append(lines, approvalMetaStyle.Render("Context: "+req.CallerContext))
	}
	if req.SensitiveNotice != "" {
		lines = append(lines, approvalMetaStyle.Render("Notice: "+req.SensitiveNotice))
	}
	lines = append(lines, approvalHintStyle.Render(approvalKeyHint(req)))

	return strings.Join(lines, "\n")
}

func renderMarkdown(src string, md *glamour.TermRenderer) string {
	if md == nil {
		return bodyStyle.Render(src)
	}
	out, err := md.Render(src)
	if err != nil {
		return bodyStyle.Render(src)
	}
	return strings.Trim(out, "\n")
}

func renderToolBlock(b renderedBlock) string {
	status := "running"
	style := toolBarStyle
	if b.toolDone {
		if b.isError {
			status = "failed"
			style = errBarStyle
		} else {
			status = "done"
		}
	}

	header := style.Render(fmt.Sprintf("@ %s [%s]", b.toolName, status))
	lines := []string{header}

	if args := render.FormatToolArgs(b.toolArgs, maxToolArgChars); args != "" {
		lines = append(lines, toolArgStyle.Render(compactText(args, 8, 3000)))
	}
	if b.toolDone && b.body != "" {
		lines = append(lines, toolOutputStyle.Render(compactToolOutput(b.body)))
	}

	return strings.Join(lines, "\n")
}

func compactToolOutput(text string) string {
	return compactText(text, maxToolOutputLines, maxToolOutputChars)
}

func compactText(text string, maxLines, maxChars int) string {
	if maxLines <= 0 || maxChars <= 0 {
		return text
	}
	runes := []rune(text)
	originalChars := len(runes)
	if originalChars > maxChars {
		headChars := maxChars * 2 / 3
		tailChars := maxChars - headChars
		text = string(runes[:headChars]) + "\n...[truncated " + fmt.Sprint(originalChars-maxChars) + " chars]...\n" + string(runes[originalChars-tailChars:])
	}
	lines := strings.Split(text, "\n")
	if len(lines) <= maxLines {
		return text
	}
	head := toolOutputHeadLines
	tail := toolOutputTailLines
	if head+tail >= maxLines {
		head = maxLines * 2 / 3
		tail = maxLines - head - 1
	}
	if head < 1 {
		head = 1
	}
	if tail < 1 {
		tail = 1
	}
	omitted := len(lines) - head - tail
	out := make([]string, 0, head+tail+1)
	out = append(out, lines[:head]...)
	out = append(out, fmt.Sprintf("...[truncated %d lines]...", omitted))
	out = append(out, lines[len(lines)-tail:]...)
	return strings.Join(out, "\n")
}
