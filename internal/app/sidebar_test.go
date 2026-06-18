package app

import (
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"
	"github.com/owenps/tdiff/internal/diff"
)

func TestSidebarStatTruncatesThousands(t *testing.T) {
	cases := map[int]string{
		999:  "+999",
		1000: "+1k",
		1999: "+1k",
		2500: "+2k",
	}
	for count, want := range cases {
		if got := sidebarStat("+", count); got != want {
			t.Fatalf("sidebarStat(%d) = %q, want %q", count, got, want)
		}
	}
}

func TestRenderSidebarUsesCompactedStats(t *testing.T) {
	m := diffPaneTestModel(false)
	lines := make([]diff.Line, 1000)
	for i := range lines {
		lines[i] = diff.Line{Kind: diff.Add, NewNo: i + 1, Text: "+x"}
	}
	m.session.SetSnapshot([]diff.File{{NewPath: "big.go", Hunks: []diff.Hunk{{Header: "@@ -0,0 +1,1000 @@", Lines: lines}}}}, "")

	out := xansi.Strip(m.renderSidebar(5))
	if !strings.Contains(out, "+1k") {
		t.Fatalf("sidebar missing compact stat:\n%s", out)
	}
	if strings.Contains(out, "+1000") {
		t.Fatalf("sidebar contains uncompact stat:\n%s", out)
	}
}
