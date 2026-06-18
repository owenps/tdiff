package app

import (
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"
	"github.com/owenps/tdiff/internal/annotations"
	"github.com/owenps/tdiff/internal/diff"
	"github.com/owenps/tdiff/internal/notes"
	"github.com/owenps/tdiff/internal/review"
)

func TestDiffPaneRendersUnifiedAnnotationAndRange(t *testing.T) {
	m := diffPaneTestModel(false)
	m.cursor.MoveLine(1, 10)
	if !m.cursor.StartRange() {
		t.Fatal("expected range to start")
	}
	m.cursor.MoveLine(1, 10)

	out := xansi.Strip(m.renderDiff(4))
	for _, want := range []string{"@@ -1 +1 @@", "╭", "●", "-old", "╰", "+new"} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered diff missing %q:\n%s", want, out)
		}
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

func diffPaneTestModel(split bool) Model {
	file := diff.File{NewPath: "foo.go", Hunks: []diff.Hunk{{Header: "@@ -1 +1 @@", Lines: []diff.Line{
		{Kind: diff.Delete, OldNo: 1, Text: "-old"},
		{Kind: diff.Add, NewNo: 1, Text: "+new"},
	}}}}
	store := &notes.Store{Notes: []notes.Note{{ID: "n1", Path: "foo.go", Side: notes.SideOld, LineStart: 1, LineEnd: 1}}}
	return Model{
		store:       store,
		annotations: annotations.NewWorkflow(store),
		cursor:      review.NewCursor([]diff.File{file}),
		width:       80,
		split:       split,
		syntax:      false,
		syntaxCache: make(map[string]string),
	}
}
