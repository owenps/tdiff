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
	viewed := &fakeViewedStore{viewed: map[string]string{"a.go": "hash"}}
	s.SetStores(viewed, fakeThreadStore{"b.go": []thread.Thread{{Path: "b.go", Side: thread.SideNew, LineStart: 1, LineEnd: 1}}})
	s.SetSnapshot([]diff.File{{NewPath: "a.go"}, {NewPath: "b.go"}, {NewPath: "c.go"}}, "hash")
	s.SetFilters(true, true)

	files := s.Files()
	if len(files) != 1 || files[0].Path() != "b.go" {
		t.Fatalf("files=%+v", files)
	}
	if len(s.AllFiles()) != 3 || s.DiffHash() != "hash" {
		t.Fatalf("all=%d hash=%q", len(s.AllFiles()), s.DiffHash())
	}
}

func TestSessionAdvanceToNextUnviewed(t *testing.T) {
	s := NewSession([]diff.File{{NewPath: "a.go"}, {NewPath: "b.go"}})
	s.SetSnapshot(s.AllFiles(), "hash")
	s.SetStores(&fakeViewedStore{viewed: map[string]string{"a.go": "hash"}}, nil)

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
	s := NewSession([]diff.File{{NewPath: "a.go"}, {NewPath: "b.go"}})
	s.SetStores(store, nil)
	s.SetSnapshot(s.AllFiles(), "hash")

	result, err := s.ToggleViewed()
	if err != nil {
		t.Fatal(err)
	}
	if !result.Viewed || !result.Advanced || s.CurrentPath() != "b.go" || !store.IsViewed("a.go", "hash") {
		t.Fatalf("result=%+v path=%q viewed=%v", result, s.CurrentPath(), store.IsViewed("a.go", "hash"))
	}

	s.ToggleHideViewed()
	result, err = s.ToggleViewed()
	if err != nil {
		t.Fatal(err)
	}
	if !result.Viewed || len(s.Files()) != 0 || !store.IsViewed("b.go", "hash") {
		t.Fatalf("result=%+v files=%d viewed=%v", result, len(s.Files()), store.IsViewed("b.go", "hash"))
	}
}
