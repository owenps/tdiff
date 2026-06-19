package review

import (
	"testing"

	"github.com/owenps/tdiff/internal/diff"
)

type fakeViewedStore struct {
	viewed map[string]string
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
	s.SetFilterSources(
		viewed,
		func(path string) int {
			if path == "b.go" {
				return 1
			}
			return 0
		},
	)
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
	s.SetFilterSources(&fakeViewedStore{viewed: map[string]string{"a.go": "hash"}}, nil)

	if !s.AdvanceToNextUnviewed() || s.CurrentPath() != "b.go" {
		t.Fatalf("path=%q", s.CurrentPath())
	}
}

func TestSessionToggleViewedOwnsFilteringAndAdvance(t *testing.T) {
	store := &fakeViewedStore{}
	s := NewSession([]diff.File{{NewPath: "a.go"}, {NewPath: "b.go"}})
	s.SetFilterSources(store, nil)
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
