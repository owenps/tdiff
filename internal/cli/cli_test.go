package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/owenps/tdiff/internal/diff"
	"github.com/owenps/tdiff/internal/snapshot"
	"github.com/owenps/tdiff/internal/thread"
)

func TestMainHelpPointsAgentsToAgentHelp(t *testing.T) {
	got := mainHelpText()
	for _, want := range []string{"tdiff agent help", "tdiff review watch", "--json"} {
		if !strings.Contains(got, want) {
			t.Fatalf("main help missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "tdiff events") {
		t.Fatalf("main help exposes legacy events command:\n%s", got)
	}
}

func TestNonInteractiveTUIHelpSteersAgentsToCLI(t *testing.T) {
	got := nonInteractiveTUIHelp()
	for _, want := range []string{"interactive TUI", "tdiff agent --help", "tdiff --help"} {
		if !strings.Contains(got, want) {
			t.Fatalf("non-interactive help missing %q:\n%s", want, got)
		}
	}
}

func TestAgentHelpContainsWorkflowAndExplicitEventKeys(t *testing.T) {
	got := agentHelpText()
	for _, want := range []string{"tdiff agent inbox --json", "tdiff review watch", "tdiff review events --json", "tdiff thread show <thread_id> --json", "thread_id=", "body_preview=", "Do not approve"} {
		if !strings.Contains(got, want) {
			t.Fatalf("agent help missing %q:\n%s", want, got)
		}
	}
}

func TestExtractAgentInboxLimitAllowsLimitAnywhere(t *testing.T) {
	limit, args, err := extractAgentInboxLimit([]string{"5", "--json", "--base", "main"})
	if err != nil {
		t.Fatal(err)
	}
	if limit != 5 || strings.Join(args, " ") != "--json --base main" {
		t.Fatalf("limit=%d args=%q", limit, args)
	}
}

func TestBuildAgentInboxFiltersSortsAndLimits(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	store := &thread.Store{Threads: []thread.Thread{
		{ID: "agent", Path: "a.go", Side: thread.SideNew, LineStart: 1, Messages: []thread.Message{{Actor: thread.ActorHuman, Body: "done"}, {Actor: thread.ActorAgent, Body: "fixed"}}, Status: thread.StatusOpen, UpdatedAt: now.Add(2 * time.Minute)},
		{ID: "human", Path: "a.go", Side: thread.SideNew, LineStart: 2, Messages: []thread.Message{{Actor: thread.ActorHuman, Body: "please fix"}}, Status: thread.StatusOpen, UpdatedAt: now},
		{ID: "stale", Path: "missing.go", Side: thread.SideNew, LineStart: 1, Messages: []thread.Message{{Actor: thread.ActorHuman, Body: "stale"}}, Status: thread.StatusOpen, UpdatedAt: now.Add(3 * time.Minute)},
		{ID: "resolved", Path: "a.go", Side: thread.SideNew, LineStart: 3, Messages: []thread.Message{{Actor: thread.ActorHuman, Body: "resolved"}}, Status: thread.StatusResolved, UpdatedAt: now.Add(4 * time.Minute)},
	}}
	snap := snapshot.Snapshot{Files: []diff.File{{NewPath: "a.go", Hunks: []diff.Hunk{{Lines: []diff.Line{{Kind: diff.Add, NewNo: 1}, {Kind: diff.Add, NewNo: 2}, {Kind: diff.Add, NewNo: 3}}}}}}}

	idx := buildAgentInbox(store, snap, 1)
	if len(idx.Threads) != 1 || idx.Threads[0].Thread.ID != "human" || idx.Threads[0].BodyPreview != "please fix" {
		t.Fatalf("inbox = %+v", idx.Threads)
	}
}

func TestEventTextLineUsesExplicitKeys(t *testing.T) {
	line := []byte(`{"id":"E1","type":"thread.created","thread_id":"T1","source":"github","actor":"alice","path":"internal/foo.go","side":"new","line_start":3,"line_end":5,"diff_hash":"abc123","body":"rename this\nextra detail","created_at":"2026-06-21T00:00:00Z"}`)

	got, err := eventTextLine(line)
	if err != nil {
		t.Fatal(err)
	}
	want := `E1 thread.created thread_id=T1 source=github actor=alice path="internal/foo.go" line_start=3 line_end=5 side=new diff_hash=abc123 body_preview="rename this"`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestEventTextLineOmitsEmptyFields(t *testing.T) {
	line := []byte(`{"id":"E2","type":"review.approved","actor":"human","diff_hash":"abc123","created_at":"2026-06-21T00:00:00Z"}`)

	got, err := eventTextLine(line)
	if err != nil {
		t.Fatal(err)
	}
	want := `E2 review.approved actor=human diff_hash=abc123`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestFreshnessUsesLegacySingleLineThread(t *testing.T) {
	files := []diff.File{{NewPath: "foo.go", Hunks: []diff.Hunk{{Lines: []diff.Line{{Kind: diff.Add, NewNo: 3}}}}}}
	thread := thread.Thread{Path: "foo.go", Side: thread.SideNew, Line: 3}

	if got := freshness(thread, files); got != "current" {
		t.Fatalf("freshness = %q, want current", got)
	}
}
