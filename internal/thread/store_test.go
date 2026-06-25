package thread

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	gh "github.com/owenps/tdiff/internal/github"
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

func TestAddGeneratesShortThreadIDs(t *testing.T) {
	store := tempStoreForStoreTest(t)
	if err := store.Add(Thread{Path: "a.go", Side: SideNew, Line: 1, Messages: []Message{{Actor: ActorHuman, Body: "first"}}}); err != nil {
		t.Fatal(err)
	}
	if err := store.Add(Thread{Path: "b.go", Side: SideNew, Line: 1, Messages: []Message{{Actor: ActorHuman, Body: "second"}}}); err != nil {
		t.Fatal(err)
	}
	if store.Threads[0].ID != "T1" || store.Threads[1].ID != "T2" {
		t.Fatalf("ids = %q, %q", store.Threads[0].ID, store.Threads[1].ID)
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

func TestUnreadForHumanTracksNonHumanLatestReply(t *testing.T) {
	store := tempStoreForStoreTest(t)
	if err := store.Add(Thread{ID: "n1", Path: "a.go", Side: SideNew, Line: 1, Messages: []Message{{Actor: ActorHuman, Body: "first"}}}); err != nil {
		t.Fatal(err)
	}
	if UnreadForHuman(store.Threads[0]) {
		t.Fatal("human-created thread should start read")
	}
	if err := store.Reply("n1", Message{Actor: ActorAgent, Body: "agent"}); err != nil {
		t.Fatal(err)
	}
	if !UnreadForHuman(store.Threads[0]) {
		t.Fatal("agent reply should be unread")
	}
	if err := store.MarkThreadRead("n1"); err != nil {
		t.Fatal(err)
	}
	if UnreadForHuman(store.Threads[0]) {
		t.Fatal("marked thread should be read")
	}
	if err := store.Reply("n1", Message{Actor: ActorHuman, Body: "human"}); err != nil {
		t.Fatal(err)
	}
	if UnreadForHuman(store.Threads[0]) {
		t.Fatal("human reply should stay read")
	}
}

func TestClearThreadsRemovesAnnotationsAndReads(t *testing.T) {
	store := tempStoreForStoreTest(t)
	if err := store.Add(Thread{ID: "n1", Path: "a.go", Side: SideNew, Line: 1, Messages: []Message{{Actor: ActorHuman, Body: "first"}}}); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkThreadRead("n1"); err != nil {
		t.Fatal(err)
	}
	count, err := store.ClearThreads()
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 || len(store.Threads) != 0 || len(store.ThreadReads) != 0 {
		t.Fatalf("count=%d threads=%d reads=%d", count, len(store.Threads), len(store.ThreadReads))
	}
}

func TestSyncGitHubThreadsPreservesReadUntilNewReply(t *testing.T) {
	store := tempStoreForStoreTest(t)
	pr := gh.AttachedPR{Owner: "o", Repo: "r", Number: 1}
	first := gh.Thread{ID: "gh1", Path: "a.go", Line: 3, Side: "RIGHT", Comments: []gh.Comment{{ID: "c1", Body: "one", CreatedAt: time.Now(), UpdatedAt: time.Now()}}}
	if _, err := store.SyncGitHubThreads(pr, []gh.Thread{first}); err != nil {
		t.Fatal(err)
	}
	if !UnreadForHuman(store.Threads[0]) {
		t.Fatal("new github thread should be unread")
	}
	if err := store.MarkThreadRead("github:gh1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SyncGitHubThreads(pr, []gh.Thread{first}); err != nil {
		t.Fatal(err)
	}
	if UnreadForHuman(store.Threads[0]) {
		t.Fatal("unchanged github thread should stay read")
	}
	second := first
	second.Comments = append(second.Comments, gh.Comment{ID: "c2", Body: "two", CreatedAt: time.Now(), UpdatedAt: time.Now()})
	if _, err := store.SyncGitHubThreads(pr, []gh.Thread{second}); err != nil {
		t.Fatal(err)
	}
	if !UnreadForHuman(store.Threads[0]) {
		t.Fatal("new github reply should be unread")
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
