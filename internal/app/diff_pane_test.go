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

func TestThreadCardTitleShowsGitHubSource(t *testing.T) {
	pane := diffPane{}
	got := pane.threadCardTitle(thread.Thread{Source: thread.SourceGitHub, Messages: []thread.Message{{ID: "m1"}, {ID: "m2"}, {ID: "m3"}}})
	if got != "github · 2 replies" {
		t.Fatalf("title = %q", got)
	}
}

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

func TestDiffPaneRendersUnreadThreadStartInRail(t *testing.T) {
	m := diffPaneTestModel(false)
	m.store.Threads[0].Messages = []thread.Message{{ID: "m1", Actor: thread.ActorAgent, Body: "fixed"}}
	m.session.SetStores(m.store, m.store)

	out := xansi.Strip(m.renderDiff(4))
	if !strings.Contains(out, "●") {
		t.Fatalf("rendered diff missing unread thread start:\n%s", out)
	}
}

func TestDiffPaneRendersReadThreadStartInRail(t *testing.T) {
	m := diffPaneTestModel(false)
	m.store.Threads[0].Messages = []thread.Message{{ID: "m1", Actor: thread.ActorAgent, Body: "fixed"}}
	m.store.Threads[0].ReadMessageID = "m1"
	m.session.SetStores(m.store, m.store)

	out := xansi.Strip(m.renderDiff(4))
	if !strings.Contains(out, "○") {
		t.Fatalf("rendered diff missing read thread start:\n%s", out)
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

func TestDiffPaneRendersInlineThreadMultilineRepliesFull(t *testing.T) {
	m := diffPaneTestModel(false)
	m.width = 100
	m.store.Threads[0].Messages = []thread.Message{
		{Actor: thread.ActorHuman, Body: "first line\nsecond line\nthird line"},
		{Actor: thread.ActorAgent, Body: "reply line\nextra detail"},
		{Actor: thread.ActorHuman, Body: "final line"},
	}
	m.session.SetStores(m.store, m.store)

	out := xansi.Strip(m.renderDiff(16))
	for _, want := range []string{"you  first line", "second line", "third line", "agent  reply line", "extra detail", "final line"} {
		if !strings.Contains(out, want) {
			t.Fatalf("inline thread missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "…") || strings.Contains(out, "older") {
		t.Fatalf("inline thread unexpectedly compacted:\n%s", out)
	}
}

func TestDiffPaneRendersInlineThreadMarkdown(t *testing.T) {
	m := diffPaneTestModel(false)
	m.width = 100
	m.store.Threads[0].Messages = []thread.Message{{Actor: thread.ActorHuman, Body: "# Heading\n\n> quote\n- item `ctx`\n[docs](https://example.test)"}}
	m.session.SetStores(m.store, m.store)

	out := xansi.Strip(m.renderDiff(12))
	for _, want := range []string{"Heading", "┃ quote", "- item ctx", "docs (https://example.test)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("inline markdown missing %q:\n%s", want, out)
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

func TestSelectedUnreadThreadIsMarkedRead(t *testing.T) {
	m := diffPaneTestModel(false)
	m.store.Threads[0].Messages = []thread.Message{{ID: "m1", Actor: thread.ActorAgent, Body: "reply"}}
	m.session.SetStores(m.store, m.store)
	m.session.MoveLine(1, 10)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	got := updated.(Model)
	if got.store.Threads[0].ReadMessageID != "m1" {
		t.Fatalf("read message = %q, want m1", got.store.Threads[0].ReadMessageID)
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

func TestSplitSelectionHighlightsWholeVisualRow(t *testing.T) {
	file := diff.File{NewPath: "foo.go", Hunks: []diff.Hunk{{Header: "@@ -1,3 +1,3 @@", Lines: []diff.Line{
		{Kind: diff.Context, OldNo: 1, NewNo: 1, Text: " before"},
		{Kind: diff.Delete, OldNo: 2, Text: "-old"},
		{Kind: diff.Add, NewNo: 2, Text: "+new"},
		{Kind: diff.Context, OldNo: 3, NewNo: 3, Text: " after"},
	}}}}
	store := &thread.Store{}
	m := Model{store: store, threads: threadworkflow.NewWorkflow(store), session: review.NewSession([]diff.File{file}), width: 80, height: 12, split: true, syntax: false, syntaxCache: make(map[string]string)}
	m.session.JumpToIndex(0, 4, m.bodyHeight())
	m.moveLine(-1)

	rows := m.splitNavForCurrentFile().rows
	pane := m.diffPane(m.diffWidth())
	oldSelected, newSelected, _, _ := pane.splitRowSideState(rows[2])
	if !oldSelected || !newSelected {
		t.Fatalf("split replacement row selection old=%t new=%t", oldSelected, newSelected)
	}
}

func TestSplitSelectionHighlightsEmptySide(t *testing.T) {
	file := diff.File{NewPath: "foo.go", Hunks: []diff.Hunk{{Header: "@@ -1,4 +1,3 @@", Lines: []diff.Line{
		{Kind: diff.Context, OldNo: 1, NewNo: 1, Text: " before"},
		{Kind: diff.Delete, OldNo: 2, Text: "-old one"},
		{Kind: diff.Delete, OldNo: 3, Text: "-old two"},
		{Kind: diff.Add, NewNo: 2, Text: "+new"},
		{Kind: diff.Context, OldNo: 4, NewNo: 3, Text: " after"},
	}}}}
	store := &thread.Store{}
	m := Model{store: store, threads: threadworkflow.NewWorkflow(store), session: review.NewSession([]diff.File{file}), width: 80, height: 12, split: true, syntax: false, syntaxCache: make(map[string]string)}
	m.session.JumpToIndex(0, 3, m.bodyHeight())

	rows := m.splitNavForCurrentFile().rows
	pane := m.diffPane(m.diffWidth())
	oldSelected, newSelected, _, _ := pane.splitRowSideState(rows[3])
	if !oldSelected || !newSelected {
		t.Fatalf("split empty-side row selection old=%t new=%t", oldSelected, newSelected)
	}
}

func TestSplitRangeRenderingMarksOnlyRangeSide(t *testing.T) {
	file := diff.File{NewPath: "foo.go", Hunks: []diff.Hunk{{Header: "@@ -1,3 +1,3 @@", Lines: []diff.Line{
		{Kind: diff.Context, OldNo: 1, NewNo: 1, Text: " before"},
		{Kind: diff.Delete, OldNo: 2, Text: "-old"},
		{Kind: diff.Add, NewNo: 2, Text: "+new"},
		{Kind: diff.Context, OldNo: 3, NewNo: 3, Text: " after"},
	}}}}
	store := &thread.Store{}
	m := Model{store: store, threads: threadworkflow.NewWorkflow(store), session: review.NewSession([]diff.File{file}), width: 80, height: 12, split: true, syntax: false, syntaxCache: make(map[string]string)}
	m.session.JumpToIndex(0, 2, m.bodyHeight())
	if result := m.session.ToggleRange(); !result.Started {
		t.Fatalf("range toggle=%+v", result)
	}
	m.moveLine(1)

	rows := m.splitNavForCurrentFile().rows
	pane := m.diffPane(m.diffWidth())
	_, replacementNewSelected, replacementOldRange, replacementNewRange := pane.splitRowSideState(rows[2])
	if !replacementOldRange || replacementNewRange || replacementNewSelected {
		t.Fatalf("replacement side state oldRange=%t newRange=%t newSelected=%t", replacementOldRange, replacementNewRange, replacementNewSelected)
	}
	contextOldSelected, contextNewSelected, contextOldRange, contextNewRange := pane.splitRowSideState(rows[3])
	if !contextOldSelected || contextNewSelected || !contextOldRange || contextNewRange {
		t.Fatalf("context side state oldSelected=%t newSelected=%t oldRange=%t newRange=%t", contextOldSelected, contextNewSelected, contextOldRange, contextNewRange)
	}
}

func TestSplitRangeNavigationStaysOnStartSide(t *testing.T) {
	file := diff.File{NewPath: "foo.go", Hunks: []diff.Hunk{{Header: "@@ -1,3 +1,3 @@", Lines: []diff.Line{
		{Kind: diff.Context, OldNo: 1, NewNo: 1, Text: " before"},
		{Kind: diff.Delete, OldNo: 2, Text: "-old"},
		{Kind: diff.Add, NewNo: 2, Text: "+new"},
		{Kind: diff.Context, OldNo: 3, NewNo: 3, Text: " after"},
	}}}}
	store := &thread.Store{}
	m := Model{store: store, threads: threadworkflow.NewWorkflow(store), session: review.NewSession([]diff.File{file}), width: 80, height: 12, split: true, syntax: false, syntaxCache: make(map[string]string)}
	m.session.JumpToIndex(0, 2, m.bodyHeight())
	if result := m.session.ToggleRange(); !result.Started {
		t.Fatalf("range toggle=%+v", result)
	}

	m.moveLine(1)
	if got := m.session.LineIndex(); got != 4 {
		t.Fatalf("split range j moved to line %d, want old-side context line 4", got)
	}
	target, err := m.rangeTarget()
	if err != nil {
		t.Fatal(err)
	}
	if target.Side != thread.SideOld || target.LineStart != 2 || target.LineEnd != 3 {
		t.Fatalf("target=%+v", target)
	}
}

func TestSplitRangeNavigationFromNewSideSkipsOldCounterpart(t *testing.T) {
	file := diff.File{NewPath: "foo.go", Hunks: []diff.Hunk{{Header: "@@ -1,3 +1,3 @@", Lines: []diff.Line{
		{Kind: diff.Context, OldNo: 1, NewNo: 1, Text: " before"},
		{Kind: diff.Delete, OldNo: 2, Text: "-old"},
		{Kind: diff.Add, NewNo: 2, Text: "+new"},
		{Kind: diff.Context, OldNo: 3, NewNo: 3, Text: " after"},
	}}}}
	store := &thread.Store{}
	m := Model{store: store, threads: threadworkflow.NewWorkflow(store), session: review.NewSession([]diff.File{file}), width: 80, height: 12, split: true, syntax: false, syntaxCache: make(map[string]string)}
	m.session.JumpToIndex(0, 3, m.bodyHeight())
	if result := m.session.ToggleRange(); !result.Started {
		t.Fatalf("range toggle=%+v", result)
	}

	m.moveLine(-1)
	if got := m.session.LineIndex(); got != 1 {
		t.Fatalf("split range k moved to line %d, want new-side context line 1", got)
	}
	target, err := m.rangeTarget()
	if err != nil {
		t.Fatal(err)
	}
	if target.Side != thread.SideNew || target.LineStart != 1 || target.LineEnd != 2 {
		t.Fatalf("target=%+v", target)
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

func TestComposerControlKeys(t *testing.T) {
	m := diffPaneTestModel(false)
	m.composing = true
	m.editor.SetValue("reply body")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	m = updated.(Model)
	if got := m.editor.LineInfo().CharOffset; got != 0 {
		t.Fatalf("ctrl+a cursor = %d, want 0", got)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	m = updated.(Model)
	if got := m.editor.LineInfo().CharOffset; got != len("reply body") {
		t.Fatalf("ctrl+e cursor = %d, want end", got)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = updated.(Model)
	if got := m.editor.Value(); got != "" || !m.composing {
		t.Fatalf("ctrl+c value=%q composing=%t, want cleared and composing", got, m.composing)
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

func TestSelectedWrappedSyntaxIntralineFixture(t *testing.T) {
	raw := wrappedSyntaxIntralineFixture()
	plain := xansi.Strip(raw)
	if !strings.Contains(plain, "changed") || !strings.Contains(plain, "eleven") {
		t.Fatalf("wrapped fixture lost text:\n%s", plain)
	}
	if !strings.Contains(raw, "38;5;") {
		t.Fatalf("wrapped fixture missing syntax highlighting:\n%s", visibleANSI(raw))
	}
	if !strings.Contains(raw, "48;5;"+string(addChangedBg)) {
		t.Fatalf("wrapped fixture missing intraline highlight:\n%s", visibleANSI(raw))
	}
}

func wrappedSyntaxIntralineFixture() string {
	file := diff.File{NewPath: "foo.go", Hunks: []diff.Hunk{{Header: "@@ -1 +1 @@", Lines: []diff.Line{
		{Kind: diff.Delete, OldNo: 1, Text: `-const message = "one two three four five six seven old nine ten eleven"`},
		{Kind: diff.Add, NewNo: 1, Text: `+const message = "one two three four five six seven changed nine ten eleven"`},
	}}}}
	store := &thread.Store{}
	m := Model{
		store:          store,
		threads:        threadworkflow.NewWorkflow(store),
		session:        review.NewSession([]diff.File{file}),
		wrapCursorLine: true,
		width:          44,
		syntax:         true,
		syntaxCache:    make(map[string]string),
	}
	m.session.JumpToIndex(0, 2, 10)
	return m.diffPane(32).Render(10)
}

func visibleANSI(s string) string {
	return strings.ReplaceAll(s, "\x1b", "␛")
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

func TestWrapSelectedBodyKeepsIntralineHighlight(t *testing.T) {
	pane := diffPane{width: 18, syntax: true, path: "foo.go", syntaxCache: make(map[string]string)}
	raw := pane.wrapSelectedBody("", diff.Add, "func example() int { return changed }", true, false, true, 12, "func example() int { return old }")
	if !strings.Contains(raw, "48;5;"+string(addChangedBg)) {
		t.Fatalf("wrapped intraline highlight missing:\n%q", raw)
	}
	if !strings.Contains(raw, "38;5;") {
		t.Fatalf("wrapped syntax highlighting missing:\n%q", raw)
	}
	if strings.Contains(xansi.Strip(raw), "…") || !strings.Contains(xansi.Strip(raw), "changed") {
		t.Fatalf("wrapped body lost text:\n%s", xansi.Strip(raw))
	}
}

func TestDiffPaneWrapsSelectedSyntaxLineWithoutBlankRows(t *testing.T) {
	file := diff.File{NewPath: "foo.go", Hunks: []diff.Hunk{{Header: "@@ -1 +1 @@", Lines: []diff.Line{
		{Kind: diff.Add, NewNo: 1, Text: "+\tmeta := fmt.Sprintf(\"width=%d diff_width=%d height=%d split=%t syntax=%t wrap=%t file=%s line_index=%d\\n\", m.width, m.diffWidth(), m.height, m.split, m.syntax, m.wrapCursorLine, m.currentPath(), m.session.LineIndex())"},
	}}}}
	store := &thread.Store{}
	m := Model{store: store, threads: threadworkflow.NewWorkflow(store), session: review.NewSession([]diff.File{file}), width: 96, hideSidebar: false, wrapCursorLine: true, syntax: true, syntaxCache: make(map[string]string)}
	m.session.JumpToIndex(0, 1, 10)
	plain := xansi.Strip(m.renderDiff(10))
	if strings.Contains(plain, "diff_   \n                                                        \n") {
		t.Fatalf("wrapped syntax line contains blank row:\n%s", plain)
	}
	for _, want := range []string{"meta := fmt.Sprintf", "width=%d height", "m.diffWidth()"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("wrapped syntax line missing %q:\n%s", want, plain)
		}
	}
}

func TestDiffPaneWrapsSelectedTabbedSyntaxLineWithoutPostWrap(t *testing.T) {
	file := diff.File{NewPath: "foo.go", Hunks: []diff.Hunk{{Header: "@@ -1 +1 @@", Lines: []diff.Line{
		{Kind: diff.Context, OldNo: 1, NewNo: 1, Text: " \t\tlinePrefix := railCell(railGlyph(marker, rangeGlyph), true, inRange) + selectedStyle.Render(\" \") + rest + diffSignView(diffSign, kind, true, inRange)"},
	}}}}
	store := &thread.Store{}
	m := Model{store: store, threads: threadworkflow.NewWorkflow(store), session: review.NewSession([]diff.File{file}), width: 96, hideSidebar: false, wrapCursorLine: true, syntax: true, syntaxCache: make(map[string]string)}
	m.session.JumpToIndex(0, 1, 10)
	plain := xansi.Strip(m.renderDiff(10))
	if strings.Contains(plain, "\narker,") || strings.Contains(plain, "\t") {
		t.Fatalf("tabbed wrapped syntax line post-wrapped:\n%s", plain)
	}
	if !strings.Contains(plain, "linePrefix :=") || !strings.Contains(plain, "marker,") {
		t.Fatalf("tabbed wrapped syntax line lost text:\n%s", plain)
	}
}

func TestWrapSelectedBodyDoesNotEmbedExtraNewlines(t *testing.T) {
	pane := diffPane{width: 56, syntax: true, path: "foo.go", syntaxCache: make(map[string]string)}
	prefix := selectedStyle.Render("              ")
	raw := pane.wrapSelectedBody(prefix, diff.Add, `return p.wrapSelectedBody(linePrefix, kind, body, true, inRange, syntaxOK, bodyW, "")`, true, false, true, 40, `return p.wrapSelectedBody(linePrefix, kind, body, true, inRange, syntaxOK, bodyW)`)
	for _, line := range strings.Split(raw, "\n") {
		if xansi.StringWidth(line) > pane.width {
			t.Fatalf("wrapped line too wide (%d):\n%s", xansi.StringWidth(line), visibleANSI(raw))
		}
	}
	if got := strings.Count(raw, "\n") + 1; got != len(strings.Split(xansi.Strip(raw), "\n")) {
		t.Fatalf("embedded newlines in ANSI parts:\n%s", visibleANSI(raw))
	}
}

func TestWrappedBodyPartsAppliesHighlightAfterWrapping(t *testing.T) {
	pane := diffPane{width: 20, syntax: true, path: "foo.go", syntaxCache: make(map[string]string)}
	parts := pane.wrappedBodyParts(diff.Add, "seven changed nine", true, false, true, 20, "seven old nine")
	got := strings.Join(parts, "\n")
	visible := visibleANSI(got)
	if !strings.Contains(visible, "48;5;"+string(addChangedBg)+"m") || !strings.Contains(xansi.Strip(got), "changed") {
		t.Fatalf("highlight not applied after wrapping:\n%s", visible)
	}
}

func TestANSIBackgroundSpanPreservesForeground(t *testing.T) {
	base := "\x1b[38;5;209mabcdef\x1b[0m"
	got := withANSIBackgroundSpan(base, 2, 4, addChangedBg, "")
	if xansi.Strip(got) != "abcdef" {
		t.Fatalf("span changed text: %q", got)
	}
	if !strings.Contains(got, "38;5;209m") || !strings.Contains(got, "48;5;"+string(addChangedBg)) {
		t.Fatalf("span lost foreground or background: %q", got)
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
