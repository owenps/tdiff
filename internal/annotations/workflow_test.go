package annotations

import (
	"testing"

	"github.com/owenps/tdiff/internal/annotate"
	"github.com/owenps/tdiff/internal/diff"
)

type fakeStore struct {
	annotations []annotate.Annotation
	added       annotate.Annotation
	updated     string
	deleted     string
}

func (s *fakeStore) Add(n annotate.Annotation) error {
	s.added = n
	s.annotations = append(s.annotations, n)
	return nil
}
func (s *fakeStore) UpdateBody(id, body string) error { s.updated = id + ":" + body; return nil }
func (s *fakeStore) Delete(id string) error           { s.deleted = id; return nil }
func (s *fakeStore) AnnotationsFor(path string) []annotate.Annotation {
	var out []annotate.Annotation
	for _, n := range s.annotations {
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
	store := &fakeStore{annotations: []annotate.Annotation{{ID: "n1", Path: "foo.go", Side: annotate.SideNew, LineStart: 10, LineEnd: 12}}}
	w := NewWorkflow(store)

	annotation, ok := w.AnnotationAt("foo.go", diff.Line{Kind: diff.Add, NewNo: 11})
	if !ok || annotation.ID != "n1" {
		t.Fatalf("annotation=%+v ok=%t", annotation, ok)
	}
	if got := w.MarkerFor(annotation, diff.Line{Kind: diff.Add, NewNo: 10}); got != "●" {
		t.Fatalf("start marker=%q", got)
	}
	if got := w.MarkerFor(annotation, diff.Line{Kind: diff.Add, NewNo: 11}); got != "│" {
		t.Fatalf("continuation marker=%q", got)
	}
	if got := w.MarkerFor(annotation, diff.Line{Kind: diff.Add, NewNo: 12}); got != "╰" {
		t.Fatalf("end marker=%q", got)
	}
}

func TestSaveAddsAnnotation(t *testing.T) {
	store := &fakeStore{}
	w := NewWorkflow(store)
	err := w.Save("foo.go", "hash", "", Target{Side: annotate.SideNew, LineStart: 7, LineEnd: 8, HunkHeader: "@@", Context: "+x"}, " body ")
	if err != nil {
		t.Fatal(err)
	}
	if store.added.Path != "foo.go" || store.added.Body != "body" || store.added.LineStart != 7 || store.added.LineEnd != 8 {
		t.Fatalf("added=%+v", store.added)
	}
}

func TestSaveRejectsOverlappingAnnotation(t *testing.T) {
	store := &fakeStore{annotations: []annotate.Annotation{{ID: "n1", Path: "foo.go", Side: annotate.SideNew, LineStart: 10, LineEnd: 12}}}
	w := NewWorkflow(store)

	err := w.Save("foo.go", "hash", "", Target{Side: annotate.SideNew, LineStart: 12, LineEnd: 14, HunkHeader: "@@", Context: "+x"}, "body")
	if err == nil {
		t.Fatal("expected overlap error")
	}
}

func TestSaveAllowsSameLinesOnOppositeSide(t *testing.T) {
	store := &fakeStore{annotations: []annotate.Annotation{{ID: "n1", Path: "foo.go", Side: annotate.SideOld, LineStart: 10, LineEnd: 12}}}
	w := NewWorkflow(store)

	err := w.Save("foo.go", "hash", "", Target{Side: annotate.SideNew, LineStart: 10, LineEnd: 12, HunkHeader: "@@", Context: "+x"}, "body")
	if err != nil {
		t.Fatal(err)
	}
}
