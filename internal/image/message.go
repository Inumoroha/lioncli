package image

import (
	"encoding/base64"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/atotto/clipboard"

	"lioncli/internal/llm"
)

const (
	imageRefPrefix = "@image:"
	clipboardRef   = "@clipboard"
)

// UserMessage builds a user message from plain text plus lightweight attachment
// references. Missing attachments are kept as text notes so the model can see
// what failed without aborting the whole turn.
func UserMessage(input, baseDir string) llm.Message {
	blocks := make([]llm.ContentBlock, 0, 2)
	text, images, notes := parseRefs(input, baseDir)
	if strings.TrimSpace(text) != "" {
		blocks = append(blocks, llm.ContentBlock{Type: llm.ContentTypeText, Text: strings.TrimSpace(text)})
	}
	for _, img := range images {
		blocks = append(blocks, img)
	}
	if len(notes) > 0 {
		blocks = append(blocks, llm.ContentBlock{Type: llm.ContentTypeText, Text: strings.Join(notes, "\n")})
	}
	if len(blocks) == 0 {
		blocks = append(blocks, llm.ContentBlock{Type: llm.ContentTypeText, Text: input})
	}
	return llm.Message{Role: llm.RoleUser, Content: blocks}
}

func parseRefs(input, baseDir string) (string, []llm.ContentBlock, []string) {
	fields := strings.Fields(input)
	kept := make([]string, 0, len(fields))
	images := make([]llm.ContentBlock, 0)
	notes := make([]string, 0)

	for _, field := range fields {
		switch {
		case strings.HasPrefix(field, imageRefPrefix):
			path := strings.TrimSpace(strings.TrimPrefix(field, imageRefPrefix))
			block, err := loadImageBlock(path, baseDir)
			if err != nil {
				notes = append(notes, fmt.Sprintf("[attachment error: %s]", err))
				continue
			}
			images = append(images, block)
		case field == clipboardRef:
			text, err := clipboard.ReadAll()
			if err != nil {
				notes = append(notes, fmt.Sprintf("[clipboard error: %v]", err))
				continue
			}
			if strings.TrimSpace(text) != "" {
				kept = append(kept, text)
			}
		default:
			kept = append(kept, field)
		}
	}
	return strings.Join(kept, " "), images, notes
}

func loadImageBlock(path, baseDir string) (llm.ContentBlock, error) {
	if strings.TrimSpace(path) == "" {
		return llm.ContentBlock{}, fmt.Errorf("empty image path")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(baseDir, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return llm.ContentBlock{}, fmt.Errorf("read image %q: %w", path, err)
	}
	mimeType := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	if !strings.HasPrefix(mimeType, "image/") {
		return llm.ContentBlock{}, fmt.Errorf("%q is not an image (%s)", path, mimeType)
	}
	return llm.ImageContent(base64.StdEncoding.EncodeToString(data), mimeType), nil
}
