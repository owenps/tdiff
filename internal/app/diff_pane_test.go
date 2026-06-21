package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/owenps/tdiff/internal/diff"
	"github.com/owenps/tdiff/internal/review"
	"github.com/owenps/tdiff/internal/thread"
	"github.com/owenps/tdiff/internal/threadworkflow"
)

func TestDiffPaneRendersUnifiedThreadAndRange(t *testing.T) {
	m := diffPaneTestModel(false)
	m.session.MoveLine(1, 10)
	if !m.session.StartRange() {
		t.Fatal("expected range to start")
	}
	m.session.MoveLine(1, 10)

	out := xansi.Strip(m.renderDiff(4))
	for _, want := range []string{"@@ -1 +1 @@", "╭", "- old", "╰", "+ new"} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered diff missing %q:\n%s", want, out)
		}
	}
}

func TestDiffPaneRendersThreadStartInRail(t *testing.T) {
	m := diffPaneTestModel(false)
	out := xansi.Strip(m.renderDiff(4))
	if !strings.Contains(out, "●") {
		t.Fatalf("rendered diff missing thread start:\n%s", out)
	}
}

func TestDiffPaneRendersThreadsInlineByDefault(t *testing.T) {
	m := diffPaneTestModel(false)
	m.width = 100
	m.store.Threads[0].Messages = []thread.Message{
		{Actor: thread.ActorHuman, Body: "should this timeout?"},
		{Actor: thread.ActorAgent, Body: "fixed, added context timeout"},
	}
	m.session.SetStores(m.store, m.store)

	out := xansi.Strip(m.renderDiff(8))
	for _, want := range []string{"1 reply", "you  should this timeout?", "agent  fixed, added context timeout"} {
		if !strings.Contains(out, want) {
			t.Fatalf("inline thread missing %q:\n%s", want, out)
		}
	}
}

func TestDiffPaneHidesInlineThreads(t *testing.T) {
	m := diffPaneTestModel(false)
	m.width = 100
	m.hideInlineThreads = true
	m.store.Threads[0].Messages = []thread.Message{{Actor: thread.ActorHuman, Body: "hidden note"}}
	m.session.SetStores(m.store, m.store)

	out := xansi.Strip(m.renderDiff(8))
	if strings.Contains(out, "hidden note") {
		t.Fatalf("inline thread rendered while hidden:\n%s", out)
	}
}

func TestInlineThreadKeybindToggles(t *testing.T) {
	m := diffPaneTestModel(false)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	got := updated.(Model)
	if !got.hideInlineThreads || got.status != "inline threads: false" {
		t.Fatalf("i toggle = hideInlineThreads %t status %q", got.hideInlineThreads, got.status)
	}
}

func TestDiffPaneInlineThreadDoesNotMoveCursor(t *testing.T) {
	m := diffPaneTestModel(false)
	m.session.MoveLine(1, 10)
	_ = m.renderDiff(8)
	if got := m.session.LineIndex(); got != 1 {
		t.Fatalf("render moved cursor to %d, want 1", got)
	}
}

func TestDiffPaneRendersSelectedThreadInlineInSplit(t *testing.T) {
	m := diffPaneTestModel(true)
	m.width = 100
	m.store.Threads[0].Messages = []thread.Message{{Actor: thread.ActorHuman, Body: "split note"}}
	m.session.SetStores(m.store, m.store)
	m.session.MoveLine(1, 10)

	out := xansi.Strip(m.renderDiff(8))
	for _, want := range []string{"you  split note"} {
		if !strings.Contains(out, want) {
			t.Fatalf("split inline thread missing %q:\n%s", want, out)
		}
	}
}

func TestDiffPaneRendersSplitView(t *testing.T) {
	m := diffPaneTestModel(true)
	out := xansi.Strip(m.renderDiff(4))
	for _, want := range []string{"│", "old", "new"} {
		if !strings.Contains(out, want) {
			t.Fatalf("split diff missing %q:\n%s", want, out)
		}
	}
}

func TestDiffPaneSplitAddOnlyUsesFullWidth(t *testing.T) {
	file := diff.File{NewPath: "foo.go", Hunks: []diff.Hunk{{Header: "@@ -0,0 +1 @@", Lines: []diff.Line{
		{Kind: diff.Add, NewNo: 1, Text: "+short text fills gap visibly"},
	}}}}
	store := &thread.Store{}
	m := Model{
		store:       store,
		threads:     threadworkflow.NewWorkflow(store),
		session:     review.NewSession([]diff.File{file}),
		width:       80,
		split:       true,
		syntax:      false,
		syntaxCache: make(map[string]string),
	}

	out := xansi.Strip(m.diffPane(44).Render(3))
	if !strings.Contains(out, "fills gap") {
		t.Fatalf("add-only split line did not use full width:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "fills gap") && strings.Contains(line, "│") {
			t.Fatalf("add-only full-width line still has split gap:\n%s", out)
		}
	}
}

func TestDiffPaneSplitDeleteOnlyUsesFullWidth(t *testing.T) {
	file := diff.File{NewPath: "foo.go", Hunks: []diff.Hunk{{Header: "@@ -1 +0,0 @@", Lines: []diff.Line{
		{Kind: diff.Delete, OldNo: 1, Text: "-short text fills gap visibly"},
	}}}}
	store := &thread.Store{}
	m := Model{
		store:       store,
		threads:     threadworkflow.NewWorkflow(store),
		session:     review.NewSession([]diff.File{file}),
		width:       80,
		split:       true,
		syntax:      false,
		syntaxCache: make(map[string]string),
	}

	out := xansi.Strip(m.diffPane(44).Render(3))
	if !strings.Contains(out, "fills gap") {
		t.Fatalf("delete-only split line did not use full width:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "fills gap") && strings.Contains(line, "│") {
			t.Fatalf("delete-only full-width line still has split gap:\n%s", out)
		}
	}
}

func TestDiffPaneSplitContextUsesFullWidthForAddOnlyHunk(t *testing.T) {
	file := diff.File{NewPath: "foo.go", Hunks: []diff.Hunk{{Header: "@@ -1 +1,2 @@", Lines: []diff.Line{
		{Kind: diff.Context, OldNo: 1, NewNo: 1, Text: " context before fills gap visibly"},
		{Kind: diff.Add, NewNo: 2, Text: "+short add"},
	}}}}
	store := &thread.Store{}
	m := Model{
		store:       store,
		threads:     threadworkflow.NewWorkflow(store),
		session:     review.NewSession([]diff.File{file}),
		width:       80,
		split:       true,
		syntax:      false,
		syntaxCache: make(map[string]string),
	}

	out := xansi.Strip(m.diffPane(44).Render(4))
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "context before") && strings.Contains(line, "│") {
			t.Fatalf("pure-add hunk context still has split gap:\n%s", out)
		}
	}
}

func TestDiffPaneSplitSeparatedAddDeleteHunkUsesFullWidth(t *testing.T) {
	file := diff.File{NewPath: "foo.go", Hunks: []diff.Hunk{{Header: "@@ -1,4 +1,4 @@", Lines: []diff.Line{
		{Kind: diff.Context, OldNo: 1, NewNo: 1, Text: " context before"},
		{Kind: diff.Add, NewNo: 2, Text: "+standalone add fills gap"},
		{Kind: diff.Context, OldNo: 2, NewNo: 3, Text: " context between fills gap"},
		{Kind: diff.Delete, OldNo: 3, Text: "-standalone delete fills gap"},
		{Kind: diff.Context, OldNo: 4, NewNo: 4, Text: " context after"},
	}}}}
	store := &thread.Store{}
	m := Model{
		store:       store,
		threads:     threadworkflow.NewWorkflow(store),
		session:     review.NewSession([]diff.File{file}),
		width:       80,
		split:       true,
		syntax:      false,
		syntaxCache: make(map[string]string),
	}

	out := xansi.Strip(m.diffPane(52).Render(8))
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "fills gap") && strings.Contains(line, "│") {
			t.Fatalf("separated pure changes still have split gap:\n%s", out)
		}
	}
}

func TestDiffPaneSplitReplacementHunkStaysSplit(t *testing.T) {
	file := diff.File{NewPath: "foo.go", Hunks: []diff.Hunk{{Header: "@@ -1 +1 @@", Lines: []diff.Line{
		{Kind: diff.Delete, OldNo: 1, Text: "-old"},
		{Kind: diff.Add, NewNo: 1, Text: "+new"},
	}}}}
	store := &thread.Store{}
	m := Model{
		store:       store,
		threads:     threadworkflow.NewWorkflow(store),
		session:     review.NewSession([]diff.File{file}),
		width:       80,
		split:       true,
		syntax:      false,
		syntaxCache: make(map[string]string),
	}

	out := xansi.Strip(m.diffPane(44).Render(3))
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "old") && !strings.Contains(line, "│") {
			t.Fatalf("replacement hunk did not stay split:\n%s", out)
		}
	}
}

func TestSplitNavigationMovesByVisualRows(t *testing.T) {
	file := diff.File{NewPath: "foo.go", Hunks: []diff.Hunk{{Header: "@@ -1,3 +1,3 @@", Lines: []diff.Line{
		{Kind: diff.Context, OldNo: 1, NewNo: 1, Text: " before"},
		{Kind: diff.Delete, OldNo: 2, Text: "-old"},
		{Kind: diff.Add, NewNo: 2, Text: "+new"},
		{Kind: diff.Context, OldNo: 3, NewNo: 3, Text: " after"},
	}}}}
	store := &thread.Store{}
	m := Model{
		store:       store,
		threads:     threadworkflow.NewWorkflow(store),
		session:     review.NewSession([]diff.File{file}),
		width:       80,
		height:      12,
		split:       true,
		syntax:      false,
		syntaxCache: make(map[string]string),
	}
	m.session.JumpToIndex(0, 2, m.bodyHeight())

	m.moveLine(1)
	if got := m.session.LineIndex(); got != 4 {
		t.Fatalf("split j moved to line %d, want next visual row 4", got)
	}
	m.moveLine(-1)
	if got := m.session.LineIndex(); got != 3 {
		t.Fatalf("split k moved to line %d, want previous visual row 3", got)
	}
}

func TestDiffPaneHidesUnifiedLineNumbers(t *testing.T) {
	m := diffPaneTestModel(false)
	m.hideLineNumbers = true

	out := xansi.Strip(m.renderDiff(4))
	if strings.Contains(out, "   1") {
		t.Fatalf("unified diff still shows line numbers:\n%s", out)
	}
	for _, want := range []string{"- old", "+ new"} {
		if !strings.Contains(out, want) {
			t.Fatalf("unified diff missing %q:\n%s", want, out)
		}
	}
}

func TestDiffPaneHidesSplitLineNumbers(t *testing.T) {
	m := diffPaneTestModel(true)
	m.hideLineNumbers = true

	out := xansi.Strip(m.renderDiff(4))
	if strings.Contains(out, "   1") {
		t.Fatalf("split diff still shows line numbers:\n%s", out)
	}
	for _, want := range []string{"│", "old", "new"} {
		if !strings.Contains(out, want) {
			t.Fatalf("split diff missing %q:\n%s", want, out)
		}
	}
}

func TestLineNumberKeybindToggles(t *testing.T) {
	m := diffPaneTestModel(false)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'L'}})
	got := updated.(Model)
	if !got.hideLineNumbers || got.status != "line numbers: false" {
		t.Fatalf("L toggle = hideLineNumbers %t status %q", got.hideLineNumbers, got.status)
	}
}

func TestEditThreadRequiresLatestHumanMessage(t *testing.T) {
	m := diffPaneTestModel(false)
	m.store.Threads[0].Messages = []thread.Message{
		{Actor: thread.ActorHuman, Body: "first"},
		{Actor: thread.ActorAgent, Body: "agent reply"},
	}
	m.session.SetStores(m.store, m.store)
	m.session.MoveLine(1, 10)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	got := updated.(Model)
	if got.composing || got.status != "can only edit latest local message" {
		t.Fatalf("edit = composing %t status %q", got.composing, got.status)
	}
}

func TestEditThreadStartsWithLatestHumanMessage(t *testing.T) {
	m := diffPaneTestModel(false)
	m.store.Threads[0].Messages = []thread.Message{
		{Actor: thread.ActorHuman, Body: "first"},
		{Actor: thread.ActorHuman, Body: "latest"},
	}
	m.session.SetStores(m.store, m.store)
	m.session.MoveLine(1, 10)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	got := updated.(Model)
	if !got.composing || got.editor.Value() != "latest" || got.editor.Placeholder != "edit latest reply…" {
		t.Fatalf("edit = composing %t editor %q placeholder %q", got.composing, got.editor.Value(), got.editor.Placeholder)
	}
}

func TestReviewViewPadsToTerminalHeight(t *testing.T) {
	m := diffPaneTestModel(false)
	m.width = 80
	m.height = 20

	lines := strings.Split(m.View(), "\n")
	if len(lines) != m.height {
		t.Fatalf("view lines=%d, want %d", len(lines), m.height)
	}
}

func TestComposerUsesBrandRail(t *testing.T) {
	m := diffPaneTestModel(false)
	m.width = 80
	m.height = 20
	m.composing = true
	m.editor.SetValue("reply body")

	out := xansi.Strip(m.View())
	if !strings.Contains(out, "┃") || !strings.Contains(out, "reply body") {
		t.Fatalf("composer missing rail:\n%s", out)
	}
	if got := m.editor.FocusedStyle.Prompt.GetForeground(); got != brandColor {
		t.Fatalf("composer rail color = %v, want %v", got, brandColor)
	}
}

func TestDiffPaneWrapsOnlySelectedLineWhenEnabled(t *testing.T) {
	file := diff.File{NewPath: "foo.go", Hunks: []diff.Hunk{{Header: "@@ -1 +1 @@", Lines: []diff.Line{
		{Kind: diff.Add, NewNo: 1, Text: "+one two three four five six seven eight"},
	}}}}
	store := &thread.Store{}
	m := Model{
		store:          store,
		threads:        threadworkflow.NewWorkflow(store),
		session:        review.NewSession([]diff.File{file}),
		wrapCursorLine: true,
		width:          80,
		syntax:         false,
		syntaxCache:    make(map[string]string),
	}
	m.session.MoveLine(1, 10)

	raw := m.diffPane(28).Render(4)
	out := xansi.Strip(raw)
	if strings.Contains(out, "…") || !strings.Contains(out, "seven eight") {
		t.Fatalf("selected line not wrapped fully:\n%s", out)
	}
	if got := len(strings.Split(out, "\n")); got < 3 {
		t.Fatalf("wrapped lines=%d, want >=3:\n%s", got, out)
	}
}

func TestDiffPaneDoesNotWrapShortSelectedSplitLine(t *testing.T) {
	file := diff.File{NewPath: "foo.go", Hunks: []diff.Hunk{{Header: "@@ -0,0 +1 @@", Lines: []diff.Line{
		{Kind: diff.Add, NewNo: 1, Text: "+short"},
	}}}}
	store := &thread.Store{}
	m := Model{
		store:          store,
		threads:        threadworkflow.NewWorkflow(store),
		session:        review.NewSession([]diff.File{file}),
		wrapCursorLine: true,
		width:          80,
		split:          true,
		syntax:         false,
		syntaxCache:    make(map[string]string),
	}
	m.session.JumpToIndex(0, 1, 10)

	out := xansi.Strip(m.diffPane(28).Render(4))
	if got := len(strings.Split(out, "\n")); got != 2 {
		t.Fatalf("short selected split line wrapped to %d lines, want 2 including hunk:\n%s", got, out)
	}
}

func TestDiffPaneWrapsSelectedFullWidthSplitLineWithSyntaxOn(t *testing.T) {
	file := diff.File{NewPath: "foo.go", Hunks: []diff.Hunk{{Header: "@@ -0,0 +1 @@", Lines: []diff.Line{
		{Kind: diff.Add, NewNo: 1, Text: "+func example() { return one + two + three + four + five + six + seven + eight }"},
	}}}}
	store := &thread.Store{}
	m := Model{
		store:          store,
		threads:        threadworkflow.NewWorkflow(store),
		session:        review.NewSession([]diff.File{file}),
		wrapCursorLine: true,
		width:          80,
		split:          true,
		syntax:         true,
		syntaxCache:    make(map[string]string),
	}
	m.session.JumpToIndex(0, 1, 10)

	raw := m.diffPane(42).Render(6)
	out := xansi.Strip(raw)
	if strings.Contains(out, "…") || !strings.Contains(out, "seven") || !strings.Contains(out, "eight") {
		t.Fatalf("selected syntax split line not wrapped fully:\n%s", out)
	}
	if !strings.Contains(raw, "\x1b[38;5;") {
		t.Fatalf("selected wrapped syntax split line missing syntax highlighting:\n%q", raw)
	}
}

func TestDiffPaneWrapsSelectedFullWidthSplitLine(t *testing.T) {
	file := diff.File{NewPath: "foo.go", Hunks: []diff.Hunk{{Header: "@@ -0,0 +1 @@", Lines: []diff.Line{
		{Kind: diff.Add, NewNo: 1, Text: "+one two three four five six seven eight"},
	}}}}
	store := &thread.Store{}
	m := Model{
		store:          store,
		threads:        threadworkflow.NewWorkflow(store),
		session:        review.NewSession([]diff.File{file}),
		wrapCursorLine: true,
		width:          80,
		split:          true,
		syntax:         false,
		syntaxCache:    make(map[string]string),
	}
	m.session.JumpToIndex(0, 1, 10)

	out := xansi.Strip(m.diffPane(28).Render(4))
	if strings.Contains(out, "…") || !strings.Contains(out, "seven") || !strings.Contains(out, "eight") {
		t.Fatalf("selected full-width split line not wrapped fully:\n%s", out)
	}
	if got := len(strings.Split(out, "\n")); got < 3 {
		t.Fatalf("wrapped split lines=%d, want >=3:\n%s", got, out)
	}
}

func TestDiffPaneWrapsSelectedUnifiedLineWithSyntaxOn(t *testing.T) {
	file := diff.File{NewPath: "foo.go", Hunks: []diff.Hunk{{Header: "@@ -0,0 +1 @@", Lines: []diff.Line{
		{Kind: diff.Add, NewNo: 1, Text: "+func example() { return one + two + three + four + five + six + seven + eight }"},
	}}}}
	store := &thread.Store{}
	m := Model{
		store:          store,
		threads:        threadworkflow.NewWorkflow(store),
		session:        review.NewSession([]diff.File{file}),
		wrapCursorLine: true,
		width:          80,
		syntax:         true,
		syntaxCache:    make(map[string]string),
	}
	m.session.JumpToIndex(0, 1, 10)

	raw := m.diffPane(36).Render(6)
	out := xansi.Strip(raw)
	if strings.Contains(out, "…") || !strings.Contains(out, "seven") || !strings.Contains(out, "eight") {
		t.Fatalf("selected syntax unified line not wrapped fully:\n%s", out)
	}
	if !strings.Contains(raw, "\x1b[38;5;") {
		t.Fatalf("selected wrapped syntax unified line missing syntax highlighting:\n%q", raw)
	}
}

func TestDiffPaneWrapsSelectedReplacementSplitLine(t *testing.T) {
	file := diff.File{NewPath: "foo.go", Hunks: []diff.Hunk{{Header: "@@ -1 +1 @@", Lines: []diff.Line{
		{Kind: diff.Delete, OldNo: 1, Text: "-one two three four five six seven eight"},
		{Kind: diff.Add, NewNo: 1, Text: "+one two three four five six seven eight"},
	}}}}
	store := &thread.Store{}
	m := Model{
		store:          store,
		threads:        threadworkflow.NewWorkflow(store),
		session:        review.NewSession([]diff.File{file}),
		wrapCursorLine: true,
		width:          80,
		split:          true,
		syntax:         false,
		syntaxCache:    make(map[string]string),
	}
	m.session.JumpToIndex(0, 1, 10)

	out := xansi.Strip(m.diffPane(42).Render(6))
	if strings.Contains(out, "…") || !strings.Contains(out, "seven eight") {
		t.Fatalf("selected replacement split line not wrapped fully:\n%s", out)
	}
	if got := len(strings.Split(out, "\n")); got < 3 {
		t.Fatalf("wrapped replacement split lines=%d, want >=3:\n%s", got, out)
	}
}

func TestDiffPaneWrapsSelectedHunkHighlight(t *testing.T) {
	file := diff.File{NewPath: "foo.go", Hunks: []diff.Hunk{{Header: "@@ -1 +1 @@ one two three four five six seven eight"}}}
	store := &thread.Store{}
	m := Model{
		store:          store,
		threads:        threadworkflow.NewWorkflow(store),
		session:        review.NewSession([]diff.File{file}),
		wrapCursorLine: true,
		width:          80,
		syntax:         false,
		syntaxCache:    make(map[string]string),
	}

	raw := m.diffPane(24).Render(4)
	out := xansi.Strip(raw)
	if strings.Contains(out, "…") || !strings.Contains(out, "seven eight") {
		t.Fatalf("selected hunk not wrapped fully:\n%s", out)
	}
	for _, line := range strings.Split(raw, "\n") {
		plain := xansi.Strip(line)
		if strings.Contains(plain, "one") || strings.Contains(plain, "three") || strings.Contains(plain, "five") || strings.Contains(plain, "seven") {
			if !strings.Contains(line, "48;5;"+string(selectedBg)) {
				t.Fatalf("wrapped hunk line missing selected highlight:\n%q", line)
			}
		}
	}
}

func TestDiffPaneKeepsSyntaxHighlightingOnAddDeleteLines(t *testing.T) {
	file := diff.File{NewPath: "foo.go", Hunks: []diff.Hunk{{Header: "@@ -1 +1 @@", Lines: []diff.Line{
		{Kind: diff.Delete, OldNo: 1, Text: "-func old() {}"},
		{Kind: diff.Add, NewNo: 1, Text: "+func main() {}"},
	}}}}
	store := &thread.Store{}
	m := Model{
		store:       store,
		threads:     threadworkflow.NewWorkflow(store),
		session:     review.NewSession([]diff.File{file}),
		width:       120,
		syntax:      true,
		syntaxCache: make(map[string]string),
	}

	out := m.renderDiff(3)
	if !strings.Contains(out, "\x1b[38;5;209mfunc") {
		t.Fatalf("add/delete lines lost syntax highlighting:\n%q", out)
	}
}

func diffPaneTestModel(split bool) Model {
	editor := textarea.New()
	editor.ShowLineNumbers = false
	editor.FocusedStyle.Prompt = threadStyle
	editor.BlurredStyle.Prompt = threadStyle
	file := diff.File{NewPath: "foo.go", Hunks: []diff.Hunk{{Header: "@@ -1 +1 @@", Lines: []diff.Line{
		{Kind: diff.Delete, OldNo: 1, Text: "-old"},
		{Kind: diff.Add, NewNo: 1, Text: "+new"},
	}}}}
	store := &thread.Store{Threads: []thread.Thread{{ID: "n1", Path: "foo.go", Side: thread.SideOld, LineStart: 1, LineEnd: 1}}}
	workflow := threadworkflow.NewWorkflow(store)
	session := review.NewSession([]diff.File{file})
	session.SetStores(store, store)
	return Model{
		store:       store,
		threads:     workflow,
		session:     session,
		editor:      editor,
		width:       80,
		split:       split,
		syntax:      false,
		syntaxCache: make(map[string]string),
	}
}
