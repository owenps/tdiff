package app

import (
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"
)

func TestStatusFooterHintsHaveNoLeadingDot(t *testing.T) {
	m := diffPaneTestModel(false)
	m.width = 100

	out := xansi.Strip(m.renderStatus())
	idx := strings.Index(out, "a note")
	if idx < 0 {
		t.Fatalf("status missing footer hints:\n%s", out)
	}
	if idx >= 2 && out[idx-2:idx] == "· " {
		t.Fatalf("footer hints have leading dot:\n%s", out)
	}
}
