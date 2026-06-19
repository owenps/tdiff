package app

import (
	"fmt"
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"
	"github.com/owenps/tdiff/internal/annotate"
	"github.com/owenps/tdiff/internal/annotations"
	"github.com/owenps/tdiff/internal/diff"
	"github.com/owenps/tdiff/internal/review"
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

func TestRenderSidebarGivesAnnotationsMoreRoomWhenScreenPermits(t *testing.T) {
	var files []diff.File
	var notes []annotate.Annotation
	for i := 1; i <= 4; i++ {
		path := fmt.Sprintf("file%d.go", i)
		files = append(files, diff.File{NewPath: path, Hunks: []diff.Hunk{{Header: "@@ -0,0 +1 @@", Lines: []diff.Line{{Kind: diff.Add, NewNo: 1, Text: "+new"}}}}})
		notes = append(notes, annotate.Annotation{ID: fmt.Sprintf("n%d", i), Path: path, Side: annotate.SideNew, LineStart: 1, LineEnd: 1, Body: fmt.Sprintf("note %d", i)})
	}
	store := &annotate.Store{Annotations: notes}
	m := Model{store: store, annotations: annotations.NewWorkflow(store), session: review.NewSession(files), width: 100}

	out := xansi.Strip(m.renderSidebar(30))
	if !strings.Contains(out, "note 4") {
		t.Fatalf("sidebar annotation preview too short:\n%s", out)
	}
}

func TestSidebarAnnotationHeightUsesBoundedScreenRatio(t *testing.T) {
	if got := sidebarAnnotationHeight(20, 20); got != 10 {
		t.Fatalf("height = %d, want 10", got)
	}
	if got := sidebarAnnotationHeight(80, 20); got != sidebarAnnotationMaxRows {
		t.Fatalf("height = %d, want %d", got, sidebarAnnotationMaxRows)
	}
}
