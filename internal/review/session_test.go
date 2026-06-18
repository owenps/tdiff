package review

import (
	"testing"

	"github.com/owenps/tdiff/internal/diff"
)

func TestSessionFiltersSnapshotFiles(t *testing.T) {
	s := NewSession(nil)
	s.SetFilterSources(
		func(path, diffHash string) bool { return path == "a.go" && diffHash == "hash" },
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
	s.SetFilterSources(
		func(path, diffHash string) bool { return path == "a.go" && diffHash == "hash" },
		nil,
	)

	if !s.AdvanceToNextUnviewed() || s.CurrentPath() != "b.go" {
		t.Fatalf("path=%q", s.CurrentPath())
	}
}
