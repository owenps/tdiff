package cli

import (
	"strings"
	"testing"

	"github.com/owenps/tdiff/internal/diff"
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

func TestAgentHelpContainsWorkflowAndExplicitEventKeys(t *testing.T) {
	got := agentHelpText()
	for _, want := range []string{"tdiff review watch", "tdiff review events --json", "tdiff thread show <thread_id> --json", "thread_id=", "body_preview=", "Do not approve"} {
		if !strings.Contains(got, want) {
			t.Fatalf("agent help missing %q:\n%s", want, got)
		}
	}
}

func TestEventTextLineUsesExplicitKeys(t *testing.T) {
	line := []byte(`{"id":"E1","type":"thread.created","thread_id":"T1","actor":"human","path":"internal/foo.go","side":"new","line_start":3,"line_end":5,"diff_hash":"abc123","body":"rename this\nextra detail","created_at":"2026-06-21T00:00:00Z"}`)

	got, err := eventTextLine(line)
	if err != nil {
		t.Fatal(err)
	}
	want := `E1 thread.created thread_id=T1 actor=human path="internal/foo.go" line_start=3 line_end=5 side=new diff_hash=abc123 body_preview="rename this"`
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
