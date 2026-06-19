package annotationtarget

import (
	"testing"

	"github.com/owenps/tdiff/internal/annotate"
	"github.com/owenps/tdiff/internal/diff"
)

func TestForLineChoosesReviewSide(t *testing.T) {
	tests := []struct {
		name string
		line diff.Line
		side annotate.Side
		no   int
		ok   bool
	}{
		{name: "delete uses old side", line: diff.Line{Kind: diff.Delete, OldNo: 3}, side: annotate.SideOld, no: 3, ok: true},
		{name: "add uses new side", line: diff.Line{Kind: diff.Add, NewNo: 4}, side: annotate.SideNew, no: 4, ok: true},
		{name: "context prefers new side", line: diff.Line{Kind: diff.Context, OldNo: 3, NewNo: 4}, side: annotate.SideNew, no: 4, ok: true},
		{name: "old only falls back to old side", line: diff.Line{Kind: diff.Context, OldNo: 3}, side: annotate.SideOld, no: 3, ok: true},
		{name: "meta has no target", line: diff.Line{Kind: diff.Meta}, side: annotate.SideNew, no: 0, ok: false},
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

func TestRangeNormalizesLegacyAnnotations(t *testing.T) {
	start, end := Range(annotate.Annotation{Line: 7})
	if start != 7 || end != 7 {
		t.Fatalf("Range() = (%d, %d), want (7, 7)", start, end)
	}
}

func TestMatchesLineUsesSideAndNormalizedRange(t *testing.T) {
	annotation := annotate.Annotation{Side: annotate.SideOld, Line: 10}
	if !MatchesLine(annotation, diff.Line{Kind: diff.Delete, OldNo: 10}) {
		t.Fatal("expected old-side delete line to match")
	}
	if MatchesLine(annotation, diff.Line{Kind: diff.Add, NewNo: 10}) {
		t.Fatal("expected new-side add line not to match old-side annotation")
	}
}
