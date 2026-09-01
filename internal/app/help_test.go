package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	xansi "github.com/charmbracelet/x/ansi"
)

func TestHelpRendersAsRightPane(t *testing.T) {
	m := diffPaneTestModel(false)
	m.width = 100
	m.height = 40
	m.showHelp = true

	view := xansi.Strip(m.View())
	lines := strings.Split(view, "\n")
	if len(lines) != m.height {
		t.Fatalf("view lines=%d, want %d", len(lines), m.height)
	}
	if !strings.HasPrefix(lines[0], "foo.go") {
		t.Fatalf("diff header hidden by help pane: %q", lines[0])
	}
	helpStart := strings.Index(lines[0], "┌ help ")
	if helpStart < 0 || xansi.StringWidth(lines[0][:helpStart]) != m.reviewWidth() {
		t.Fatalf("help does not start at right pane boundary %d: %q", m.reviewWidth(), lines[0])
	}
	if !strings.Contains(view, "@@ -1 +1 @@") || !strings.Contains(view, "?          hide help") {
		t.Fatalf("view missing diff or help content:\n%s", view)
	}
	if !strings.Contains(lines[len(lines)-1], "? hide") {
		t.Fatalf("footer missing hide hint: %q", lines[len(lines)-1])
	}
}

func TestHelpPaneKeepsViewWithinTerminalWidth(t *testing.T) {
	m := diffPaneTestModel(false)
	m.width = 60
	m.height = 20
	m.showHelp = true

	lines := strings.Split(m.View(), "\n")
	for i, line := range lines[:len(lines)-1] {
		if got := xansi.StringWidth(line); got != m.width {
			t.Fatalf("line %d width=%d, want %d", i, got, m.width)
		}
	}
	if m.diffWidth() != 30 || m.helpPaneWidth() != 30 {
		t.Fatalf("narrow layout diff=%d help=%d", m.diffWidth(), m.helpPaneWidth())
	}
}

func TestHelpKeyTogglesRightPane(t *testing.T) {
	m := diffPaneTestModel(false)
	m.height = 20

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	m = updated.(Model)
	if !m.showHelp || m.helpPaneWidth() == 0 {
		t.Fatalf("help did not open: show=%t width=%d", m.showHelp, m.helpPaneWidth())
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	m = updated.(Model)
	if m.showHelp || m.helpPaneWidth() != 0 {
		t.Fatalf("help did not close: show=%t width=%d", m.showHelp, m.helpPaneWidth())
	}
}
