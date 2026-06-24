package thread

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

func TestSyncGitHubThreadsEmitsGitHubCommentEvents(t *testing.T) {
	store := tempStore(t)
	pr := gh.AttachedPR{Owner: "owenps", Repo: "tdiff", Number: 12}
	first := gh.Thread{ID: "thread1", Path: "main.go", Line: 20, Side: "RIGHT", Comments: []gh.Comment{{ID: "comment1", Body: "fix this", Author: gh.Author{Login: "alice"}, CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)}}}

	if _, err := store.SyncGitHubThreads(pr, []gh.Thread{first}); err != nil {
		t.Fatal(err)
	}
	second := first
	second.Comments = append(second.Comments, gh.Comment{ID: "reply1", Body: "done", Author: gh.Author{Login: "bob"}, CreatedAt: time.Unix(2, 0), UpdatedAt: time.Unix(2, 0)})
	if _, err := store.SyncGitHubThreads(pr, []gh.Thread{second}); err != nil {
		t.Fatal(err)
	}

	events := readStoreEvents(t, store)
	if len(events) != 2 {
		t.Fatalf("event count = %d, want 2: %#v", len(events), events)
	}
	if events[0].Type != "thread.created" || events[0].Source != SourceGitHub || events[0].Actor != Actor("alice") || events[0].Body != "fix this" {
		t.Fatalf("bad create event: %#v", events[0])
	}
	if events[1].Type != "thread.replied" || events[1].Source != SourceGitHub || events[1].Actor != Actor("bob") || events[1].Body != "done" {
		t.Fatalf("bad reply event: %#v", events[1])
	}
}

func readStoreEvents(t *testing.T, store *Store) []Event {
	t.Helper()
	b, err := os.ReadFile(store.EventsPath())
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	events := make([]Event, 0, len(lines))
	for _, line := range lines {
		var event Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	return events
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
