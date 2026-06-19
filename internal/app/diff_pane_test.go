package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/owenps/tdiff/internal/annotate"
	"github.com/owenps/tdiff/internal/annotations"
	"github.com/owenps/tdiff/internal/diff"
	"github.com/owenps/tdiff/internal/review"
)

func TestDiffPaneRendersUnifiedAnnotationAndRange(t *testing.T) {
	m := diffPaneTestModel(false)
	m.session.MoveLine(1, 10)
	if !m.session.StartRange() {
		t.Fatal("expected range to start")
	}
	m.session.MoveLine(1, 10)

	out := xansi.Strip(m.renderDiff(4))
	for _, want := range []string{"@@ -1 +1 @@", "╭", "- old", "╰", "+ new"} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered diff missing %q:\n%s", want, out)
		}
	}
}

func TestDiffPaneRendersAnnotationStartInRail(t *testing.T) {
	m := diffPaneTestModel(false)
	out := xansi.Strip(m.renderDiff(4))
	if !strings.Contains(out, "●") {
		t.Fatalf("rendered diff missing annotation start:\n%s", out)
	}
}

func TestDiffPaneRendersSplitView(t *testing.T) {
	m := diffPaneTestModel(true)
	out := xansi.Strip(m.renderDiff(4))
	for _, want := range []string{"│", "old", "new"} {
		if !strings.Contains(out, want) {
			t.Fatalf("split diff missing %q:\n%s", want, out)
		}
	}
}

func TestDiffPaneHidesUnifiedLineNumbers(t *testing.T) {
	m := diffPaneTestModel(false)
	m.hideLineNumbers = true

	out := xansi.Strip(m.renderDiff(4))
	if strings.Contains(out, "   1") {
		t.Fatalf("unified diff still shows line numbers:\n%s", out)
	}
	for _, want := range []string{"- old", "+ new"} {
		if !strings.Contains(out, want) {
			t.Fatalf("unified diff missing %q:\n%s", want, out)
		}
	}
}

func TestDiffPaneHidesSplitLineNumbers(t *testing.T) {
	m := diffPaneTestModel(true)
	m.hideLineNumbers = true

	out := xansi.Strip(m.renderDiff(4))
	if strings.Contains(out, "   1") {
		t.Fatalf("split diff still shows line numbers:\n%s", out)
	}
	for _, want := range []string{"│", "old", "new"} {
		if !strings.Contains(out, want) {
			t.Fatalf("split diff missing %q:\n%s", want, out)
		}
	}
}

func TestLineNumberKeybindToggles(t *testing.T) {
	m := diffPaneTestModel(false)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'L'}})
	got := updated.(Model)
	if !got.hideLineNumbers || got.status != "line numbers: false" {
		t.Fatalf("L toggle = hideLineNumbers %t status %q", got.hideLineNumbers, got.status)
	}
}

func TestReviewViewPadsToTerminalHeight(t *testing.T) {
	m := diffPaneTestModel(false)
	m.width = 80
	m.height = 20

	lines := strings.Split(m.View(), "\n")
	if len(lines) != m.height {
		t.Fatalf("view lines=%d, want %d", len(lines), m.height)
	}
}

func TestDiffPaneWrapsOnlySelectedLineWhenEnabled(t *testing.T) {
	file := diff.File{NewPath: "foo.go", Hunks: []diff.Hunk{{Header: "@@ -1 +1 @@", Lines: []diff.Line{
		{Kind: diff.Add, NewNo: 1, Text: "+one two three four five six seven eight"},
	}}}}
	store := &annotate.Store{}
	m := Model{
		store:          store,
		annotations:    annotations.NewWorkflow(store),
		session:        review.NewSession([]diff.File{file}),
		wrapCursorLine: true,
		width:          80,
		syntax:         false,
		syntaxCache:    make(map[string]string),
	}
	m.session.MoveLine(1, 10)

	raw := m.diffPane(28).Render(4)
	out := xansi.Strip(raw)
	if strings.Contains(out, "…") || !strings.Contains(out, "seven eight") {
		t.Fatalf("selected line not wrapped fully:\n%s", out)
	}
	if got := len(strings.Split(out, "\n")); got < 3 {
		t.Fatalf("wrapped lines=%d, want >=3:\n%s", got, out)
	}
	if !strings.Contains(raw, "\x1b[48;5;"+string(selectedBg)+"mseven") {
		t.Fatalf("wrapped continuation text missing selected highlight:\n%q", raw)
	}
}

func TestDiffPaneWrapsSelectedHunkHighlight(t *testing.T) {
	file := diff.File{NewPath: "foo.go", Hunks: []diff.Hunk{{Header: "@@ -1 +1 @@ one two three four five six seven eight"}}}
	store := &annotate.Store{}
	m := Model{
		store:          store,
		annotations:    annotations.NewWorkflow(store),
		session:        review.NewSession([]diff.File{file}),
		wrapCursorLine: true,
		width:          80,
		syntax:         false,
		syntaxCache:    make(map[string]string),
	}

	raw := m.diffPane(24).Render(4)
	out := xansi.Strip(raw)
	if strings.Contains(out, "…") || !strings.Contains(out, "seven eight") {
		t.Fatalf("selected hunk not wrapped fully:\n%s", out)
	}
	for _, line := range strings.Split(raw, "\n") {
		plain := xansi.Strip(line)
		if strings.Contains(plain, "one") || strings.Contains(plain, "three") || strings.Contains(plain, "five") || strings.Contains(plain, "seven") {
			if !strings.Contains(line, "48;5;"+string(selectedBg)) {
				t.Fatalf("wrapped hunk line missing selected highlight:\n%q", line)
			}
		}
	}
}

func TestDiffPaneKeepsSyntaxHighlightingOnAddDeleteLines(t *testing.T) {
	file := diff.File{NewPath: "foo.go", Hunks: []diff.Hunk{{Header: "@@ -1 +1 @@", Lines: []diff.Line{
		{Kind: diff.Delete, OldNo: 1, Text: "-func old() {}"},
		{Kind: diff.Add, NewNo: 1, Text: "+func main() {}"},
	}}}}
	store := &annotate.Store{}
	m := Model{
		store:       store,
		annotations: annotations.NewWorkflow(store),
		session:     review.NewSession([]diff.File{file}),
		width:       120,
		syntax:      true,
		syntaxCache: make(map[string]string),
	}

	out := m.renderDiff(3)
	if !strings.Contains(out, "\x1b[38;5;209mfunc") {
		t.Fatalf("add/delete lines lost syntax highlighting:\n%q", out)
	}
}

func diffPaneTestModel(split bool) Model {
	file := diff.File{NewPath: "foo.go", Hunks: []diff.Hunk{{Header: "@@ -1 +1 @@", Lines: []diff.Line{
		{Kind: diff.Delete, OldNo: 1, Text: "-old"},
		{Kind: diff.Add, NewNo: 1, Text: "+new"},
	}}}}
	store := &annotate.Store{Annotations: []annotate.Annotation{{ID: "n1", Path: "foo.go", Side: annotate.SideOld, LineStart: 1, LineEnd: 1}}}
	workflow := annotations.NewWorkflow(store)
	session := review.NewSession([]diff.File{file})
	session.SetStores(store, store)
	return Model{
		store:       store,
		annotations: workflow,
		session:     session,
		width:       80,
		split:       split,
		syntax:      false,
		syntaxCache: make(map[string]string),
	}
}
