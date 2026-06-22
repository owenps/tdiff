package threadtarget

import (
	"testing"

	"github.com/owenps/tdiff/internal/diff"
	"github.com/owenps/tdiff/internal/thread"
)

func TestForLineChoosesReviewSide(t *testing.T) {
	tests := []struct {
		name string
		line diff.Line
		side thread.Side
		no   int
		ok   bool
	}{
		{name: "delete uses old side", line: diff.Line{Kind: diff.Delete, OldNo: 3}, side: thread.SideOld, no: 3, ok: true},
		{name: "add uses new side", line: diff.Line{Kind: diff.Add, NewNo: 4}, side: thread.SideNew, no: 4, ok: true},
		{name: "context prefers new side", line: diff.Line{Kind: diff.Context, OldNo: 3, NewNo: 4}, side: thread.SideNew, no: 4, ok: true},
		{name: "old only falls back to old side", line: diff.Line{Kind: diff.Context, OldNo: 3}, side: thread.SideOld, no: 3, ok: true},
		{name: "meta has no target", line: diff.Line{Kind: diff.Meta}, side: thread.SideNew, no: 0, ok: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			side, no, ok := ForLine(tc.line)
			if side != tc.side || no != tc.no || ok != tc.ok {
				t.Fatalf("ForLine() = (%q, %d, %t), want (%q, %d, %t)", side, no, ok, tc.side, tc.no, tc.ok)
			}
		})
	}
}

func TestRangeNormalizesLegacyThreads(t *testing.T) {
	start, end := Range(thread.Thread{Line: 7})
	if start != 7 || end != 7 {
		t.Fatalf("Range() = (%d, %d), want (7, 7)", start, end)
	}
}

func TestMatchesLineUsesSideAndNormalizedRange(t *testing.T) {
	thread := thread.Thread{Side: thread.SideOld, Line: 10}
	if !MatchesLine(thread, diff.Line{Kind: diff.Delete, OldNo: 10}) {
		t.Fatal("expected old-side delete line to match")
	}
	if MatchesLine(thread, diff.Line{Kind: diff.Add, NewNo: 10}) {
		t.Fatal("expected new-side add line not to match old-side thread")
	}
}

func TestCurrentInFilesNormalizesLegacyThreadRange(t *testing.T) {
	files := []diff.File{{NewPath: "foo.go", Hunks: []diff.Hunk{{Lines: []diff.Line{{Kind: diff.Add, NewNo: 7}}}}}}
	thread := thread.Thread{Path: "foo.go", Side: thread.SideNew, Line: 7}

	if !CurrentInFiles(thread, files) {
		t.Fatal("expected legacy single-line thread to be current")
	}
}

func TestCurrentInFilesRequiresFullRange(t *testing.T) {
	files := []diff.File{{NewPath: "foo.go", Hunks: []diff.Hunk{{Lines: []diff.Line{{Kind: diff.Add, NewNo: 7, Text: "+one"}}}}}}
	thread := thread.Thread{Path: "foo.go", Side: thread.SideNew, LineStart: 7, LineEnd: 8, Context: "+one\n+two"}

	if CurrentInFiles(thread, files) {
		t.Fatal("partial range should be stale")
	}
}

func TestCurrentInFilesRequiresContextMatch(t *testing.T) {
	files := []diff.File{{NewPath: "foo.go", Hunks: []diff.Hunk{{Lines: []diff.Line{{Kind: diff.Add, NewNo: 7, Text: "+changed"}}}}}}
	thread := thread.Thread{Path: "foo.go", Side: thread.SideNew, LineStart: 7, LineEnd: 7, Context: "+original"}

	if CurrentInFiles(thread, files) {
		t.Fatal("same line number with changed context should be stale")
	}
}

func TestCurrentInFilesAllowsMissingLegacyContext(t *testing.T) {
	files := []diff.File{{NewPath: "foo.go", Hunks: []diff.Hunk{{Lines: []diff.Line{{Kind: diff.Add, NewNo: 7, Text: "+changed"}}}}}}
	thread := thread.Thread{Path: "foo.go", Side: thread.SideNew, LineStart: 7, LineEnd: 7}

	if !CurrentInFiles(thread, files) {
		t.Fatal("thread without stored context should use full-range line presence")
	}
}

func TestContextForRangeReturnsHunkContextOnSide(t *testing.T) {
	files := []diff.File{{NewPath: "foo.go", Hunks: []diff.Hunk{{Header: "@@ -1 +1 @@", Lines: []diff.Line{
		{Kind: diff.Delete, OldNo: 1, Text: "-old"},
		{Kind: diff.Add, NewNo: 1, Text: "+new"},
	}}}}}

	header, context, ok := ContextForRange(files, "foo.go", thread.SideOld, 1, 1)
	if !ok || header != "@@ -1 +1 @@" || context != "-old" {
		t.Fatalf("ContextForRange() = (%q, %q, %t)", header, context, ok)
	}
}
