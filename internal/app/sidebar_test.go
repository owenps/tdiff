package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"
	"github.com/owenps/tdiff/internal/diff"
	"github.com/owenps/tdiff/internal/git"
	"github.com/owenps/tdiff/internal/review"
	"github.com/owenps/tdiff/internal/snapshot"
	"github.com/owenps/tdiff/internal/thread"
	"github.com/owenps/tdiff/internal/threadworkflow"
)

type fakeViewedStore struct{ viewed map[string]string }

func (s *fakeViewedStore) MarkViewed(path, diffHash string) error {
	s.viewed[path] = diffHash
	return nil
}

func (s *fakeViewedStore) ClearViewed(path string) error {
	delete(s.viewed, path)
	return nil
}

func (s *fakeViewedStore) IsViewed(path, diffHash string) bool { return s.viewed[path] == diffHash }

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

func TestChangedFileMarkerPersistsUntilAcknowledged(t *testing.T) {
	oldFile := diff.File{NewPath: "a.go", Hunks: []diff.Hunk{{Header: "@@ -0,0 +1 @@", Lines: []diff.Line{{Kind: diff.Add, NewNo: 1, Text: "+old"}}}}}
	newFile := diff.File{NewPath: "a.go", Hunks: []diff.Hunk{{Header: "@@ -0,0 +1 @@", Lines: []diff.Line{{Kind: diff.Add, NewNo: 1, Text: "+new"}}}}}
	store := &thread.Store{}
	session := review.NewSession([]diff.File{oldFile})
	m := Model{store: store, session: session, width: 100, fileHashes: fileHashes([]diff.File{oldFile}), changedFiles: make(map[string]bool)}

	m.updateChangedFiles([]diff.File{newFile})
	out := xansi.Strip(m.renderSidebar(5))
	if !strings.Contains(out, "◆") {
		t.Fatalf("sidebar missing changed marker:\n%s", out)
	}

	m.updateChangedFiles([]diff.File{newFile})
	if !m.changedFiles["a.go"] {
		t.Fatal("unchanged refresh cleared changed marker")
	}

	m.acknowledgeFileChange("a.go")
	m.invalidateViewCache()
	out = xansi.Strip(m.renderSidebar(5))
	if strings.Contains(out, "◆") {
		t.Fatalf("sidebar still shows acknowledged marker:\n%s", out)
	}
}

func TestRestoreCursorKeepsPathAndLineAcrossRefresh(t *testing.T) {
	oldFiles := []diff.File{
		{NewPath: "a.go", Hunks: []diff.Hunk{{Header: "@@ -0,0 +1 @@", Lines: []diff.Line{{Kind: diff.Add, NewNo: 1, Text: "+a"}}}}},
		{NewPath: "b.go", Hunks: []diff.Hunk{{Header: "@@ -0,0 +1,2 @@", Lines: []diff.Line{{Kind: diff.Add, NewNo: 1, Text: "+one"}, {Kind: diff.Add, NewNo: 2, Text: "+two"}}}}},
	}
	newFiles := []diff.File{
		{NewPath: "a.go", Hunks: []diff.Hunk{{Header: "@@ -0,0 +1 @@", Lines: []diff.Line{{Kind: diff.Add, NewNo: 1, Text: "+changed"}}}}},
		oldFiles[1],
	}
	store := &thread.Store{}
	session := review.NewSession(oldFiles)
	m := Model{store: store, session: session, width: 100, height: 12}
	m.session.JumpToIndex(1, 2, m.bodyHeight())
	anchor := m.cursorAnchor()
	m.session.SetSnapshot(newFiles, "hash")
	m.restoreCursor(anchor)

	if m.currentPath() != "b.go" || m.session.LineIndex() != 2 {
		t.Fatalf("path=%q line=%d", m.currentPath(), m.session.LineIndex())
	}
}

func TestRefreshLoadedKeepsCursorAfterStoreReload(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := thread.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	oldFiles := []diff.File{
		{NewPath: "a.go", Hunks: []diff.Hunk{{Header: "@@ -0,0 +1 @@", Lines: []diff.Line{{Kind: diff.Add, NewNo: 1, Text: "+a"}}}}},
		{NewPath: "b.go", Hunks: []diff.Hunk{{Header: "@@ -0,0 +1,2 @@", Lines: []diff.Line{{Kind: diff.Add, NewNo: 1, Text: "+one"}, {Kind: diff.Add, NewNo: 2, Text: "+two"}}}}},
	}
	newFiles := []diff.File{
		{NewPath: "a.go", Hunks: []diff.Hunk{{Header: "@@ -0,0 +1 @@", Lines: []diff.Line{{Kind: diff.Add, NewNo: 1, Text: "+changed"}}}}},
		oldFiles[1],
	}
	m := Model{repo: git.Repo{Root: root}, store: store, threads: threadworkflow.NewWorkflow(store), session: review.NewSession(oldFiles), width: 100, height: 12}
	m.session.SetStores(store, store)
	m.session.JumpToIndex(1, 2, m.bodyHeight())

	m.handleRefreshLoaded(refreshLoadedMsg{snap: snapshot.Snapshot{Files: newFiles, Hash: "hash"}, offline: true, auto: true})

	if m.currentPath() != "b.go" || m.session.LineIndex() != 2 {
		t.Fatalf("path=%q line=%d", m.currentPath(), m.session.LineIndex())
	}
}

func TestCollapsedHeaderShowsViewedOnlyWhenViewed(t *testing.T) {
	file := diff.File{NewPath: "file.go", Hunks: []diff.Hunk{{Header: "@@ -0,0 +1 @@", Lines: []diff.Line{{Kind: diff.Add, NewNo: 1, Text: "+new"}}}}}
	store := &fakeViewedStore{viewed: map[string]string{}}
	session := review.NewSession([]diff.File{file})
	session.SetStores(store, nil)
	m := Model{store: &thread.Store{}, session: session, width: 80}

	if got := xansi.Strip(m.renderDiffHeader(80)); strings.Contains(got, "viewed") || strings.Contains(got, "✓") {
		t.Fatalf("unviewed header = %q", got)
	}
	if err := store.MarkViewed("file.go", diff.FileHash(file)); err != nil {
		t.Fatal(err)
	}
	if got := xansi.Strip(m.renderDiffHeader(80)); !strings.Contains(got, "✓ viewed") {
		t.Fatalf("viewed header = %q", got)
	}
}

func TestRenderSidebarShowsReplyCounts(t *testing.T) {
	file := diff.File{NewPath: "file.go", Hunks: []diff.Hunk{{Header: "@@ -0,0 +1 @@", Lines: []diff.Line{{Kind: diff.Add, NewNo: 1, Text: "+new"}}}}}
	store := &thread.Store{Threads: []thread.Thread{{ID: "n1", Path: "file.go", Side: thread.SideNew, LineStart: 1, LineEnd: 1, Messages: []thread.Message{
		{ID: "m1", Actor: thread.ActorHuman, Body: "note"},
		{ID: "m2", Actor: thread.ActorAgent, Body: "reply one"},
		{ID: "m3", Actor: thread.ActorHuman, Body: "reply two"},
	}}}}
	workflow := threadworkflow.NewWorkflow(store)
	session := review.NewSession([]diff.File{file})
	session.SetStores(store, store)
	m := Model{store: store, threads: workflow, session: session, width: 100}

	out := xansi.Strip(m.renderSidebar(16))
	if !strings.Contains(out, "↳2") {
		t.Fatalf("sidebar missing reply count:\n%s", out)
	}
	if !strings.Contains(out, "○") {
		t.Fatalf("sidebar missing read glyph:\n%s", out)
	}
}

func TestRenderSidebarShowsUnreadGlyph(t *testing.T) {
	file := diff.File{NewPath: "file.go", Hunks: []diff.Hunk{{Header: "@@ -0,0 +1 @@", Lines: []diff.Line{{Kind: diff.Add, NewNo: 1, Text: "+new"}}}}}
	store := &thread.Store{Threads: []thread.Thread{{ID: "n1", Path: "file.go", Side: thread.SideNew, LineStart: 1, LineEnd: 1, Messages: []thread.Message{
		{ID: "m1", Actor: thread.ActorHuman, Body: "note"},
		{ID: "m2", Actor: thread.ActorGitHub, Body: "github reply"},
	}}}}
	workflow := threadworkflow.NewWorkflow(store)
	session := review.NewSession([]diff.File{file})
	session.SetStores(store, store)
	m := Model{store: store, threads: workflow, session: session, width: 100}

	out := xansi.Strip(m.renderSidebar(16))
	if !strings.Contains(out, "●") {
		t.Fatalf("sidebar missing unread glyph:\n%s", out)
	}
}

func TestRenderSidebarGivesThreadsMoreRoomWhenScreenPermits(t *testing.T) {
	var files []diff.File
	var notes []thread.Thread
	for i := 1; i <= 4; i++ {
		path := fmt.Sprintf("file%d.go", i)
		files = append(files, diff.File{NewPath: path, Hunks: []diff.Hunk{{Header: "@@ -0,0 +1 @@", Lines: []diff.Line{{Kind: diff.Add, NewNo: 1, Text: "+new"}}}}})
		notes = append(notes, thread.Thread{ID: fmt.Sprintf("n%d", i), Path: path, Side: thread.SideNew, LineStart: 1, LineEnd: 1, Messages: []thread.Message{{Actor: thread.ActorHuman, Body: fmt.Sprintf("note %d", i)}}})
	}
	store := &thread.Store{Threads: notes}
	workflow := threadworkflow.NewWorkflow(store)
	session := review.NewSession(files)
	session.SetStores(store, store)
	m := Model{store: store, threads: workflow, session: session, width: 100}

	out := xansi.Strip(m.renderSidebar(30))
	if !strings.Contains(out, "note 4") {
		t.Fatalf("sidebar thread preview too short:\n%s", out)
	}
}

func TestSidebarThreadHeightUsesBoundedScreenRatio(t *testing.T) {
	if got := sidebarThreadHeight(20, 20); got != 10 {
		t.Fatalf("height = %d, want 10", got)
	}
	if got := sidebarThreadHeight(80, 20); got != sidebarThreadMaxRows {
		t.Fatalf("height = %d, want %d", got, sidebarThreadMaxRows)
	}
}
