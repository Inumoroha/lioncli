package render

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFormatStatusLine(t *testing.T) {
	got := FormatStatusLine(StatusInfo{
		Model:         "deepseek-chat",
		TotalTokens:   1530,
		ContextWindow: 128000,
		HITLEnabled:   true,
		ElapsedMillis: 1250,
	}, 80)

	for _, want := range []string{"deepseek-chat", "1.5k/128.0k", "HITL ON", "1.2s"} {
		if !strings.Contains(got, want) {
			t.Fatalf("status line missing %q: %s", want, got)
		}
	}
}

func TestFormatStatusLineTruncates(t *testing.T) {
	got := FormatStatusLine(IdleStatus("very-long-model-name", 1000, false), 10)
	if len(got) != 10 {
		t.Fatalf("expected truncated length 10, got %d: %q", len(got), got)
	}
}

func TestFormatStatusLineTruncatesUTF8Safely(t *testing.T) {
	got := FormatStatusLine(IdleStatus("模型模型", 1000, false), 5)
	if !utf8.ValidString(got) {
		t.Fatalf("status line is invalid UTF-8: %q", got)
	}
	if got != " 模型" {
		t.Fatalf("unexpected width-aware truncation: %q", got)
	}
}
