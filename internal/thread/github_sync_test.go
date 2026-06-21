package thread

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	gh "github.com/owenps/tdiff/internal/github"
)

func TestSyncGitHubThreadsUpsertsThreadAndHidesResolved(t *testing.T) {
	store := tempStore(t)
	pr := gh.AttachedPR{Owner: "owenps", Repo: "tdiff", Number: 12}
	threads := []gh.Thread{{
		ID:       "thread1",
		Path:     "main.go",
		Line:     20,
		Side:     "RIGHT",
		Resolved: true,
		Comments: []gh.Comment{{
			ID: "comment1", Body: "fix this", URL: "https://example.com", Author: gh.Author{Login: "owenps", Name: "Owen"}, CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(2, 0),
		}, {
			ID: "reply1", Body: "done", Author: gh.Author{Login: "bot"}, CreatedAt: time.Unix(3, 0), UpdatedAt: time.Unix(4, 0),
		}},
	}}

	count, err := store.SyncGitHubThreads(pr, threads)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
	if got := store.ThreadsFor("main.go"); len(got) != 0 {
		t.Fatalf("resolved thread visible: %#v", got)
	}
	if len(store.Threads) != 1 || store.Threads[0].GitHub == nil {
		t.Fatalf("missing github thread: %#v", store.Threads)
	}
	if store.Threads[0].Source != SourceGitHub || store.Threads[0].Status != StatusResolved || len(store.Threads[0].Messages) != 2 {
		t.Fatalf("bad thread: %#v", store.Threads[0])
	}
}

func TestSyncGitHubThreadsRemovesMissingAndOutdated(t *testing.T) {
	store := tempStore(t)
	pr := gh.AttachedPR{Owner: "owenps", Repo: "tdiff", Number: 12}
	store.Threads = []Thread{{ID: "github:old", Source: SourceGitHub, Path: "main.go", GitHub: &GitHubMetadata{Owner: pr.Owner, Repo: pr.Repo, PRNumber: pr.Number, ThreadID: "old"}}, {ID: "local", Source: SourceLocal, Path: "main.go"}}

	_, err := store.SyncGitHubThreads(pr, []gh.Thread{{ID: "new", Path: "main.go", Line: 1, Side: "RIGHT", Outdated: true, Comments: []gh.Comment{{ID: "c", Body: "old"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(store.Threads) != 1 || store.Threads[0].ID != "local" {
		t.Fatalf("stale github thread not removed / local removed: %#v", store.Threads)
	}
}

func tempStore(t *testing.T) *Store {
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
