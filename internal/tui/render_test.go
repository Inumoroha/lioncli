package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

func TestCompactTextKeepsShortOutput(t *testing.T) {
	in := "one\ntwo\n"
	if got := compactText(in, 10, 100); got != in {
		t.Fatalf("short output changed: got %q want %q", got, in)
	}
}

func TestCompactTextTruncatesLongOutputByLines(t *testing.T) {
	lines := make([]string, 0, 20)
	for i := 0; i < 20; i++ {
		lines = append(lines, fmt.Sprintf("line-%02d", i))
	}
	got := compactText(strings.Join(lines, "\n"), 8, 1000)
	if !strings.Contains(got, "...[truncated") {
		t.Fatalf("expected truncation marker:\n%s", got)
	}
	if !strings.Contains(got, "line-00") || !strings.Contains(got, "line-19") {
		t.Fatalf("expected head and tail to be preserved:\n%s", got)
	}
}

func TestCompactTextTruncatesLongOutputByRunes(t *testing.T) {
	in := strings.Repeat("你好", 100)
	got := compactText(in, 100, 50)
	if !strings.Contains(got, "chars") {
		t.Fatalf("expected char truncation marker:\n%s", got)
	}
	if strings.Contains(got, "\ufffd") {
		t.Fatalf("truncation produced invalid replacement rune:\n%s", got)
	}
}

func TestCommandMenuMatchesSlashPrefix(t *testing.T) {
	m := newCommandMenuTestModel("/")

	matches := m.commandMenuMatches()
	if len(matches) == 0 {
		t.Fatal("expected slash command matches")
	}
	if matches[0].Name != "/help" {
		t.Fatalf("first command = %q, want /help", matches[0].Name)
	}
}

func TestCommandMenuMatchesCommandNamePrefixFirst(t *testing.T) {
	m := newCommandMenuTestModel("/m")

	matches := m.commandMenuMatches()
	if len(matches) < 2 {
		t.Fatalf("expected at least /mcp and /memory, got %d matches", len(matches))
	}
	if matches[0].Name != "/mcp" || matches[1].Name != "/memory" {
		t.Fatalf("prefix matches not first: got %q, %q", matches[0].Name, matches[1].Name)
	}
}

func TestCommandMenuHidesAfterWhitespace(t *testing.T) {
	for _, input := range []string{"/plan ", "/plan goal"} {
		m := newCommandMenuTestModel(input)
		if matches := m.commandMenuMatches(); len(matches) != 0 {
			t.Fatalf("input %q produced %d matches, want none", input, len(matches))
		}
	}
}

func TestCommandMenuTabCompletesSelection(t *testing.T) {
	m := newCommandMenuTestModel("/mem")

	updated, handled := m.handleCommandMenuKey(tea.KeyMsg{Type: tea.KeyTab})
	if !handled {
		t.Fatal("tab was not handled")
	}
	if got := updated.input.Value(); got != "/memory " {
		t.Fatalf("completed input = %q, want /memory ", got)
	}
}

func TestCommandMenuEnterCompletesPartialOnly(t *testing.T) {
	m := newCommandMenuTestModel("/hel")

	updated, handled := m.handleCommandMenuKey(tea.KeyMsg{Type: tea.KeyEnter})
	if !handled {
		t.Fatal("enter on partial command was not handled")
	}
	if got := updated.input.Value(); got != "/help " {
		t.Fatalf("completed input = %q, want /help ", got)
	}

	m = newCommandMenuTestModel("/help")
	if _, handled := m.handleCommandMenuKey(tea.KeyMsg{Type: tea.KeyEnter}); handled {
		t.Fatal("enter on exact command should fall through to normal submit")
	}
}

func newCommandMenuTestModel(value string) model {
	input := textarea.New()
	input.SetValue(value)
	return model{input: input}
}
