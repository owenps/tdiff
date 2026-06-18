package review

import (
	"testing"

	"github.com/owenps/tdiff/internal/annotate"
	"github.com/owenps/tdiff/internal/diff"
)

func TestCursorJumpsHunksAndLines(t *testing.T) {
	c := NewCursor([]diff.File{{NewPath: "foo.go", Hunks: []diff.Hunk{
		{Header: "@@ -1 +1 @@", Lines: []diff.Line{{Kind: diff.Context, OldNo: 1, NewNo: 1}}},
		{Header: "@@ -10 +10 @@", Lines: []diff.Line{{Kind: diff.Add, NewNo: 10}}},
	}}})

	if !c.NextHunk(3) || c.LineIndex() != 2 {
		t.Fatalf("line index after next hunk=%d", c.LineIndex())
	}
	if !c.JumpToFileLine(10, 3) || c.LineIndex() != 3 {
		t.Fatalf("line index after jump=%d", c.LineIndex())
	}
}

func TestCursorRangeLines(t *testing.T) {
	c := NewCursor([]diff.File{{NewPath: "foo.go", Hunks: []diff.Hunk{{Header: "@@", Lines: []diff.Line{
		{Kind: diff.Context, OldNo: 1, NewNo: 1},
		{Kind: diff.Add, NewNo: 2},
	}}}}})
	c.MoveLine(1, 10)
	if !c.StartRange() {
		t.Fatal("expected range to start")
	}
	c.MoveLine(1, 10)
	if !c.InActiveRange(1) || !c.InActiveRange(2) {
		t.Fatalf("range not active across selected indexes")
	}
	if got := len(c.RangeLines()); got != 2 {
		t.Fatalf("range lines=%d", got)
	}
}

func TestCursorFiltersViewedAndAnnotationsOnly(t *testing.T) {
	files := []diff.File{{NewPath: "a.go"}, {NewPath: "b.go"}, {NewPath: "c.go"}}
	filtered := FilterFiles(files, FileFilter{
		HideViewed:      true,
		AnnotationsOnly: true,
		DiffHash:        "hash",
		IsViewed: func(path, diffHash string) bool {
			return path == "a.go" && diffHash == "hash"
		},
		AnnotationCount: func(path string) int {
			if path == "b.go" {
				return 1
			}
			return 0
		},
	})
	if len(filtered) != 1 || filtered[0].Path() != "b.go" {
		t.Fatalf("filtered=%+v", filtered)
	}
}

func TestCursorJumpsAnnotations(t *testing.T) {
	c := NewCursor([]diff.File{
		{NewPath: "a.go", Hunks: []diff.Hunk{{Header: "@@", Lines: []diff.Line{{Kind: diff.Add, NewNo: 1}}}}},
		{NewPath: "b.go", Hunks: []diff.Hunk{{Header: "@@", Lines: []diff.Line{{Kind: diff.Add, NewNo: 2}}}}},
	})
	annotationsForPath := func(path string) []annotate.Annotation {
		if path == "b.go" {
			return []annotate.Annotation{{ID: "n1", Path: path, Side: annotate.SideNew, LineStart: 2, LineEnd: 2}}
		}
		return nil
	}
	idx, total, ok := c.JumpAnnotation(1, 10, annotationsForPath)
	if !ok || idx != 1 || total != 1 || c.FileIndex() != 1 || c.LineIndex() != 1 {
		t.Fatalf("idx=%d total=%d ok=%t file=%d line=%d", idx, total, ok, c.FileIndex(), c.LineIndex())
	}
}
