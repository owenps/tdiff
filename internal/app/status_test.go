package app

import (
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"
	"github.com/owenps/tdiff/internal/git"
	gh "github.com/owenps/tdiff/internal/github"
)

func TestStatusFooterHintsHaveNoLeadingDot(t *testing.T) {
	m := diffPaneTestModel(false)
	m.width = 100

	out := xansi.Strip(m.renderStatus())
	idx := strings.Index(out, "a add thread")
	if idx < 0 {
		t.Fatalf("status missing footer hints:\n%s", out)
	}
	if idx >= 2 && out[idx-2:idx] == "· " {
		t.Fatalf("footer hints have leading dot:\n%s", out)
	}
}

func TestStatusShowsPRNumberWithoutStatus(t *testing.T) {
	m := diffPaneTestModel(false)
	m.width = 100
	m.store.GitHub = &gh.AttachedPR{Number: 12, Status: gh.PRStatusBlocked}

	out := xansi.Strip(m.renderStatus())
	if !strings.Contains(out, "PR #12") || strings.Contains(out, "blocked") {
		t.Fatalf("status = %q", out)
	}
}

func TestStatusShowsCompareTarget(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want string
	}{
		{name: "branch base", cfg: Config{Base: "origin/main"}, want: "origin/main"},
		{name: "staged", cfg: Config{Mode: git.ModeStaged}, want: "HEAD"},
		{name: "unstaged", cfg: Config{Mode: git.ModeUnstaged}, want: "staged"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := diffPaneTestModel(false)
			m.width = 100
			m.cfg = tc.cfg

			out := xansi.Strip(m.renderStatus())
			if !strings.HasPrefix(out, tc.want+" · ") {
				t.Fatalf("status = %q, want prefix %q", out, tc.want)
			}
		})
	}
}
