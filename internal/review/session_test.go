package review

import (
	"testing"

	"github.com/owenps/tdiff/internal/diff"
	"github.com/owenps/tdiff/internal/thread"
)

type fakeViewedStore struct {
	viewed map[string]string
}

type fakeThreadStore map[string][]thread.Thread

func (s fakeThreadStore) ThreadsFor(path string) []thread.Thread {
	return s[path]
}

func (s *fakeViewedStore) MarkViewed(path, diffHash string) error {
	if s.viewed == nil {
		s.viewed = make(map[string]string)
	}
	s.viewed[path] = diffHash
	return nil
}

func (s *fakeViewedStore) ClearViewed(path string) error {
	delete(s.viewed, path)
	return nil
}

func (s *fakeViewedStore) IsViewed(path, diffHash string) bool {
	return s.viewed[path] == diffHash
}

func TestSessionFiltersSnapshotFiles(t *testing.T) {
	s := NewSession(nil)
	files := []diff.File{{NewPath: "a.go"}, {NewPath: "b.go"}, {NewPath: "c.go"}}
	viewed := &fakeViewedStore{viewed: map[string]string{"a.go": diff.FileHash(files[0])}}
	s.SetStores(viewed, fakeThreadStore{"b.go": []thread.Thread{{Path: "b.go", Side: thread.SideNew, LineStart: 1, LineEnd: 1}}})
	s.SetSnapshot(files, "hash")
	s.SetFilters(true, true)

	filtered := s.Files()
	if len(filtered) != 1 || filtered[0].Path() != "b.go" {
		t.Fatalf("files=%+v", filtered)
	}
	if len(s.AllFiles()) != 3 || s.DiffHash() != "hash" {
		t.Fatalf("all=%d hash=%q", len(s.AllFiles()), s.DiffHash())
	}
}

func TestSessionAdvanceToNextUnviewed(t *testing.T) {
	files := []diff.File{{NewPath: "a.go"}, {NewPath: "b.go"}}
	s := NewSession(files)
	s.SetSnapshot(s.AllFiles(), "hash")
	s.SetStores(&fakeViewedStore{viewed: map[string]string{"a.go": diff.FileHash(files[0])}}, nil)

	if !s.AdvanceToNextUnviewed() || s.CurrentPath() != "b.go" {
		t.Fatalf("path=%q", s.CurrentPath())
	}
}

func TestSessionLineWindowOwnsRangeGlyphs(t *testing.T) {
	s := NewSession([]diff.File{{NewPath: "a.go", Hunks: []diff.Hunk{{Header: "@@", Lines: []diff.Line{
		{Kind: diff.Delete, OldNo: 1, Text: "-old"},
		{Kind: diff.Add, NewNo: 1, Text: "+new"},
	}}}}})
	s.MoveLine(1, 10)
	if result := s.ToggleRange(); !result.Started {
		t.Fatalf("range toggle=%+v", result)
	}
	s.MoveLine(1, 10)

	window := s.LineWindow(10)
	if !window.RangeActive || !window.InActiveRange(1) || !window.InActiveRange(2) || window.InActiveRange(0) {
		t.Fatalf("window range=%+v", window)
	}
	if window.RangeGlyph(1) != "╭" || window.RangeGlyph(2) != "╰" {
		t.Fatalf("glyphs=%q/%q", window.RangeGlyph(1), window.RangeGlyph(2))
	}
}

func TestSessionSelectedThreadUsesStore(t *testing.T) {
	s := NewSession([]diff.File{{NewPath: "a.go", Hunks: []diff.Hunk{{Header: "@@", Lines: []diff.Line{{Kind: diff.Add, NewNo: 7, Text: "+new"}}}}}})
	s.SetStores(nil, fakeThreadStore{"a.go": []thread.Thread{{ID: "n1", Path: "a.go", Side: thread.SideNew, LineStart: 7, LineEnd: 7}}})
	s.MoveLine(1, 10)

	thread, ok := s.SelectedThread()
	if !ok || thread.ID != "n1" {
		t.Fatalf("thread=%+v ok=%t", thread, ok)
	}
}

func TestSessionToggleViewedOwnsFilteringAndAdvance(t *testing.T) {
	store := &fakeViewedStore{}
	files := []diff.File{{NewPath: "a.go"}, {NewPath: "b.go"}}
	s := NewSession(files)
	s.SetStores(store, nil)
	s.SetSnapshot(s.AllFiles(), "hash")

	result, err := s.ToggleViewed()
	if err != nil {
		t.Fatal(err)
	}
	if !result.Viewed || !result.Advanced || s.CurrentPath() != "b.go" || !store.IsViewed("a.go", diff.FileHash(files[0])) {
		t.Fatalf("result=%+v path=%q viewed=%v", result, s.CurrentPath(), store.IsViewed("a.go", diff.FileHash(files[0])))
	}

	s.ToggleHideViewed()
	result, err = s.ToggleViewed()
	if err != nil {
		t.Fatal(err)
	}
	if !result.Viewed || len(s.Files()) != 0 || !store.IsViewed("b.go", diff.FileHash(files[1])) {
		t.Fatalf("result=%+v files=%d viewed=%v", result, len(s.Files()), store.IsViewed("b.go", diff.FileHash(files[1])))
	}
}

func TestSessionViewedUsesPerFileHash(t *testing.T) {
	store := &fakeViewedStore{}
	a := diff.File{NewPath: "a.go", Hunks: []diff.Hunk{{Header: "@@ -1 +1 @@", Lines: []diff.Line{{Kind: diff.Add, NewNo: 1, Text: "+a"}}}}}
	b := diff.File{NewPath: "b.go", Hunks: []diff.Hunk{{Header: "@@ -1 +1 @@", Lines: []diff.Line{{Kind: diff.Add, NewNo: 1, Text: "+b"}}}}}

	s := NewSession([]diff.File{a})
	s.SetStores(store, nil)
	s.SetSnapshot([]diff.File{a}, "snapshot-1")
	if _, err := s.ToggleViewed(); err != nil {
		t.Fatal(err)
	}

	s.SetSnapshot([]diff.File{a, b}, "snapshot-2")
	if !s.IsViewed("a.go") {
		t.Fatal("unchanged file lost viewed mark after unrelated file changed")
	}

	changedA := diff.File{NewPath: "a.go", Hunks: []diff.Hunk{{Header: "@@ -1 +1 @@", Lines: []diff.Line{{Kind: diff.Add, NewNo: 1, Text: "+changed"}}}}}
	s.SetSnapshot([]diff.File{changedA, b}, "snapshot-3")
	if s.IsViewed("a.go") {
		t.Fatal("changed file stayed viewed")
	}
}
