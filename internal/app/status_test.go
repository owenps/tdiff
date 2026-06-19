package app

import (
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"
	"github.com/owenps/tdiff/internal/git"
)

func TestStatusFooterHintsHaveNoLeadingDot(t *testing.T) {
	m := diffPaneTestModel(false)
	m.width = 100

	out := xansi.Strip(m.renderStatus())
	idx := strings.Index(out, "a annotate")
	if idx < 0 {
		t.Fatalf("status missing footer hints:\n%s", out)
	}
	if idx >= 2 && out[idx-2:idx] == "· " {
		t.Fatalf("footer hints have leading dot:\n%s", out)
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
