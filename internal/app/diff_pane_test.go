package app

import (
	"strings"
	"testing"

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

	out := xansi.Strip(m.diffPane(28).Render(4))
	if strings.Contains(out, "…") || !strings.Contains(out, "seven eight") {
		t.Fatalf("selected line not wrapped fully:\n%s", out)
	}
	if got := len(strings.Split(out, "\n")); got < 3 {
		t.Fatalf("wrapped lines=%d, want >=3:\n%s", got, out)
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
	return Model{
		store:       store,
		annotations: annotations.NewWorkflow(store),
		session:     review.NewSession([]diff.File{file}),
		width:       80,
		split:       split,
		syntax:      false,
		syntaxCache: make(map[string]string),
	}
}
