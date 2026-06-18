package annotations

import (
	"testing"

	"github.com/owenps/tdiff/internal/diff"
	"github.com/owenps/tdiff/internal/notes"
)

type fakeStore struct {
	notes   []notes.Note
	added   notes.Note
	updated string
	deleted string
}

func (s *fakeStore) Add(n notes.Note) error {
	s.added = n
	s.notes = append(s.notes, n)
	return nil
}
func (s *fakeStore) UpdateBody(id, body string) error { s.updated = id + ":" + body; return nil }
func (s *fakeStore) Delete(id string) error           { s.deleted = id; return nil }
func (s *fakeStore) NotesFor(path string) []notes.Note {
	var out []notes.Note
	for _, n := range s.notes {
		if n.Path == path {
			out = append(out, n)
		}
	}
	return out
}

func TestTargetForRangeRejectsMixedSides(t *testing.T) {
	w := NewWorkflow(&fakeStore{})
	_, err := w.TargetForRange([]DiffLine{
		{Line: diff.Line{Kind: diff.Delete, OldNo: 3, Text: "-old"}, HunkHeader: "@@"},
		{Line: diff.Line{Kind: diff.Add, NewNo: 3, Text: "+new"}, HunkHeader: "@@"},
	})
	if err == nil {
		t.Fatal("expected mixed old/new range error")
	}
}

func TestAnnotationAtAndMarkerForRange(t *testing.T) {
	store := &fakeStore{notes: []notes.Note{{ID: "n1", Path: "foo.go", Side: notes.SideNew, LineStart: 10, LineEnd: 12}}}
	w := NewWorkflow(store)

	note, ok := w.AnnotationAt("foo.go", diff.Line{Kind: diff.Add, NewNo: 11})
	if !ok || note.ID != "n1" {
		t.Fatalf("annotation=%+v ok=%t", note, ok)
	}
	if got := w.MarkerFor(note, diff.Line{Kind: diff.Add, NewNo: 10}); got != "●" {
		t.Fatalf("start marker=%q", got)
	}
	if got := w.MarkerFor(note, diff.Line{Kind: diff.Add, NewNo: 11}); got != "│" {
		t.Fatalf("continuation marker=%q", got)
	}
	if got := w.MarkerFor(note, diff.Line{Kind: diff.Add, NewNo: 12}); got != "╰" {
		t.Fatalf("end marker=%q", got)
	}
}

func TestSaveAddsAnnotation(t *testing.T) {
	store := &fakeStore{}
	w := NewWorkflow(store)
	err := w.Save("foo.go", "hash", "", Target{Side: notes.SideNew, LineStart: 7, LineEnd: 8, HunkHeader: "@@", Context: "+x"}, " body ")
	if err != nil {
		t.Fatal(err)
	}
	if store.added.Path != "foo.go" || store.added.Body != "body" || store.added.LineStart != 7 || store.added.LineEnd != 8 {
		t.Fatalf("added=%+v", store.added)
	}
}
