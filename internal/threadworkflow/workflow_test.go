package threadworkflow

import (
	"testing"

	"github.com/owenps/tdiff/internal/diff"
	"github.com/owenps/tdiff/internal/thread"
)

type fakeStore struct {
	threads []thread.Thread
	added   thread.Thread
	updated string
	deleted string
}

func (s *fakeStore) Add(n thread.Thread) error {
	s.added = n
	s.threads = append(s.threads, n)
	return nil
}
func (s *fakeStore) UpdateFirstMessage(id, body string) error {
	s.updated = id + ":" + body
	return nil
}
func (s *fakeStore) Delete(id string) error { s.deleted = id; return nil }
func (s *fakeStore) ThreadsFor(path string) []thread.Thread {
	var out []thread.Thread
	for _, n := range s.threads {
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

func TestThreadAtAndMarkerForRange(t *testing.T) {
	store := &fakeStore{threads: []thread.Thread{{ID: "n1", Path: "foo.go", Side: thread.SideNew, LineStart: 10, LineEnd: 12}}}
	w := NewWorkflow(store)

	thread, ok := w.ThreadAt("foo.go", diff.Line{Kind: diff.Add, NewNo: 11})
	if !ok || thread.ID != "n1" {
		t.Fatalf("thread=%+v ok=%t", thread, ok)
	}
	if got := w.MarkerFor(thread, diff.Line{Kind: diff.Add, NewNo: 10}); got != "●" {
		t.Fatalf("start marker=%q", got)
	}
	if got := w.MarkerFor(thread, diff.Line{Kind: diff.Add, NewNo: 11}); got != "│" {
		t.Fatalf("continuation marker=%q", got)
	}
	if got := w.MarkerFor(thread, diff.Line{Kind: diff.Add, NewNo: 12}); got != "╰" {
		t.Fatalf("end marker=%q", got)
	}
}

func TestSaveAddsThread(t *testing.T) {
	store := &fakeStore{}
	w := NewWorkflow(store)
	err := w.Save("foo.go", "hash", "", Target{Side: thread.SideNew, LineStart: 7, LineEnd: 8, HunkHeader: "@@", Context: "+x"}, " body ")
	if err != nil {
		t.Fatal(err)
	}
	if store.added.Path != "foo.go" || thread.Body(store.added) != "body" || store.added.LineStart != 7 || store.added.LineEnd != 8 {
		t.Fatalf("added=%+v", store.added)
	}
}

func TestSaveRejectsOverlappingThread(t *testing.T) {
	store := &fakeStore{threads: []thread.Thread{{ID: "n1", Path: "foo.go", Side: thread.SideNew, LineStart: 10, LineEnd: 12}}}
	w := NewWorkflow(store)

	err := w.Save("foo.go", "hash", "", Target{Side: thread.SideNew, LineStart: 12, LineEnd: 14, HunkHeader: "@@", Context: "+x"}, "body")
	if err == nil {
		t.Fatal("expected overlap error")
	}
}

func TestSaveAllowsSameLinesOnOppositeSide(t *testing.T) {
	store := &fakeStore{threads: []thread.Thread{{ID: "n1", Path: "foo.go", Side: thread.SideOld, LineStart: 10, LineEnd: 12}}}
	w := NewWorkflow(store)

	err := w.Save("foo.go", "hash", "", Target{Side: thread.SideNew, LineStart: 10, LineEnd: 12, HunkHeader: "@@", Context: "+x"}, "body")
	if err != nil {
		t.Fatal(err)
	}
}
