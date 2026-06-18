package review

import (
	"testing"

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
