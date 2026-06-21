package thread

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenUsesGitDirDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, ".git", "tdiff", "review.json")
	if store.path != want {
		t.Fatalf("path = %q, want %q", store.path, want)
	}
}

func TestOpenUsesWorktreeGitDirFile(t *testing.T) {
	root := t.TempDir()
	gitDir := filepath.Join(t.TempDir(), ".git", "worktrees", "example")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: "+gitDir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(gitDir, "tdiff", "review.json")
	if store.path != want {
		t.Fatalf("path = %q, want %q", store.path, want)
	}
}

func TestHumanThreadInvalidatesApprovalForSameDiff(t *testing.T) {
	store := tempStoreForStoreTest(t)
	if err := store.Approve("hash"); err != nil {
		t.Fatal(err)
	}
	if err := store.Add(Thread{Path: "a.go", Side: SideNew, Line: 1, DiffHash: "hash", Messages: []Message{{Actor: ActorHuman, Body: "please fix"}}}); err != nil {
		t.Fatal(err)
	}
	if store.ReviewStatus("hash") != "pending" {
		t.Fatalf("review status = %q, want pending", store.ReviewStatus("hash"))
	}
}

func TestUnapproveClearsApproval(t *testing.T) {
	store := tempStoreForStoreTest(t)
	if err := store.Approve("hash"); err != nil {
		t.Fatal(err)
	}
	if err := store.Unapprove("hash"); err != nil {
		t.Fatal(err)
	}
	if store.ReviewStatus("hash") != "pending" {
		t.Fatalf("review status = %q, want pending", store.ReviewStatus("hash"))
	}
}

func TestUpdateLatestMessageEditsOnlyLastHumanMessage(t *testing.T) {
	store := tempStoreForStoreTest(t)
	if err := store.Add(Thread{ID: "n1", Path: "a.go", Side: SideNew, Line: 1, DiffHash: "hash", Messages: []Message{{Actor: ActorHuman, Body: "first"}}}); err != nil {
		t.Fatal(err)
	}
	if err := store.Reply("n1", Message{Actor: ActorHuman, Body: "latest"}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateLatestMessage("n1", "edited"); err != nil {
		t.Fatal(err)
	}
	got := store.Threads[0].Messages
	if got[0].Body != "first" || got[1].Body != "edited" {
		t.Fatalf("messages = %+v", got)
	}
}

func TestUpdateLatestMessageRejectsLatestAgentMessage(t *testing.T) {
	store := tempStoreForStoreTest(t)
	if err := store.Add(Thread{ID: "n1", Path: "a.go", Side: SideNew, Line: 1, DiffHash: "hash", Messages: []Message{{Actor: ActorHuman, Body: "first"}}}); err != nil {
		t.Fatal(err)
	}
	if err := store.Reply("n1", Message{Actor: ActorAgent, Body: "agent"}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateLatestMessage("n1", "edited"); err == nil {
		t.Fatal("expected latest local message error")
	}
	if got := store.Threads[0].Messages[1].Body; got != "agent" {
		t.Fatalf("latest body = %q, want agent", got)
	}
}

func tempStoreForStoreTest(t *testing.T) *Store {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	return store
}
