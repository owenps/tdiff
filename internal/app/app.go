package app

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/owenps/tdiff/internal/diff"
	"github.com/owenps/tdiff/internal/git"
	gh "github.com/owenps/tdiff/internal/github"
	"github.com/owenps/tdiff/internal/review"
	"github.com/owenps/tdiff/internal/snapshot"
	"github.com/owenps/tdiff/internal/thread"
	"github.com/owenps/tdiff/internal/threadtarget"
	"github.com/owenps/tdiff/internal/threadworkflow"
)

type Config struct {
	Base             string
	Mode             git.Mode
	IgnoreWhitespace bool
	Offline          bool
	Debug            bool
}

type Model struct {
	repo          git.Repo
	cfg           Config
	compareTarget string
	store         *thread.Store
	threads       threadworkflow.Workflow

	session review.Session

	width  int
	height int

	loadingFrame      int
	pendingKey        string
	jumpPrompt        bool
	jumpInput         string
	split             bool
	syntax            bool
	contextDim        bool
	wrapCursorLine    bool
	hideLineNumbers   bool
	showHelp          bool
	hideSidebar       bool
	hideInlineThreads bool
	composing         bool
	editingThreadID   string
	replyingThreadID  string
	pendingTarget     threadworkflow.Target
	editor            textarea.Model
	prPicker          prPicker
	prAttaching       bool
	refreshing        bool
	status            string
	statusID          int
	composerBaseView  string
	viewCache         map[string]string
	statsCache        map[string]diffStats
	syntaxCache       map[string]string
	splitHunkCache    map[string]map[string]bool
	splitNavCache     map[string]splitNav
	fileHashes        map[string]string
	changedFiles      map[string]bool
	splitOffset       int
}

func New(ctx context.Context, cfg Config) (Model, error) {
	repo, err := git.Open(ctx)
	if err != nil {
		return Model{}, err
	}
	store, err := thread.Open(repo.Root)
	if err != nil {
		return Model{}, err
	}
	m := Model{repo: repo, cfg: cfg, store: store, threads: threadworkflow.NewWorkflow(store), session: review.NewSession(nil), syntax: true, contextDim: true, viewCache: make(map[string]string), statsCache: make(map[string]diffStats), syntaxCache: make(map[string]string), splitHunkCache: make(map[string]map[string]bool), splitNavCache: make(map[string]splitNav), fileHashes: make(map[string]string), changedFiles: make(map[string]bool)}
	m.session.SetStores(store, store)
	m.editor = textarea.New()
	m.editor.Placeholder = "write a review comment…"
	m.editor.CharLimit = 4000
	m.editor.SetHeight(5)
	m.editor.ShowLineNumbers = false
	m.editor.FocusedStyle.Prompt = threadStyle
	m.editor.BlurredStyle.Prompt = threadStyle
	if err := m.reload(ctx); err != nil {
		return Model{}, err
	}
	return m, nil
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(textarea.Blink, loadingSpinnerTick(), autoRefreshTick(), m.initialGitHubRefreshCmd(), tea.SetWindowTitle("∓ tdiff"))
}

func (m Model) initialGitHubRefreshCmd() tea.Cmd {
	if m.cfg.Offline {
		return nil
	}
	return m.refreshProjectCmd(true)
}

type clearStatusMsg struct{ id int }
type loadingSpinnerTickMsg struct{}
type autoRefreshTickMsg struct{}
type prListLoadedMsg struct {
	prs []gh.PullRequest
	err error
}
type prAttachLoadedMsg struct {
	pr        gh.AttachedPR
	threads   []gh.Thread
	err       error
	threadErr error
}
type refreshLoadedMsg struct {
	snap          snapshot.Snapshot
	compareTarget string
	pr            *gh.AttachedPR
	threads       []gh.Thread
	err           error
	threadErr     error
	offline       bool
	noPR          bool
	auto          bool
}

type threadStatusChangedMsg struct {
	id     string
	status thread.Status
	err    error
}

func loadingSpinnerTick() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg {
		return loadingSpinnerTickMsg{}
	})
}

func autoRefreshTick() tea.Cmd {
	return tea.Tick(60*time.Second, func(time.Time) tea.Msg {
		return autoRefreshTickMsg{}
	})
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if next, cmd, ok := m.handleAsyncMsg(msg); ok {
		next.markSelectedThreadRead()
		return next, cmd
	}
	if m.composing {
		return m.updateComposer(msg)
	}

	previousStatus := m.status
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.updateWindowSize(msg)
		m.markSelectedThreadRead()
	case tea.KeyMsg:
		next, cmd := m.updateKey(msg, previousStatus)
		if nextModel, ok := next.(Model); ok {
			nextModel.markSelectedThreadRead()
			return nextModel, cmd
		}
		return next, cmd
	}
	return m, m.statusToastCmd(previousStatus)
}

func (m Model) View() string {
	if !m.ready() {
		return m.loadingView()
	}
	if m.composing {
		view := m.composerBaseView
		if view == "" {
			view = m.reviewView()
		}
		return view + "\n" + m.renderComposer()
	}
	if m.prPicker.Active() {
		return m.reviewView() + "\n" + m.prPicker.View(m.width, m.loadingFrame)
	}

	view := m.reviewView()
	if m.showHelp {
		return overlay(view, m.renderHelp(), m.width, m.height)
	}
	return view
}

func (m Model) renderComposer() string {
	return m.editor.View() + "\n" + dimStyle.Render("⌥+enter save · esc cancel")
}

func (m Model) ready() bool {
	return m.width > 0 && m.height > 0
}

func (m Model) loadingView() string {
	frame := loadingSpinnerFrames[m.loadingFrame%len(loadingSpinnerFrames)]
	return threadStyle.Render(frame) + " " + dimStyle.Render("opening tdiff…")
}

func (m Model) reviewView() string {
	if len(m.session.Files()) == 0 {
		if len(m.session.AllFiles()) > 0 {
			return dimStyle.Render("no files match filters · press u/m") + "\n"
		}
		return dimStyle.Render("clean tree · nothing to review") + "\n"
	}
	bodyHeight := m.bodyHeight()
	diffHeight := bodyHeight
	var header string
	if m.hideSidebar {
		diffHeight = max(1, bodyHeight-1)
		header = m.renderDiffHeader(m.width)
	}
	diffPane := m.renderDiff(diffHeight)
	body := diffPane
	if m.hideSidebar {
		body = header + "\n" + diffPane
	} else {
		sidebar := m.renderSidebar(bodyHeight)
		body = lipgloss.JoinHorizontal(lipgloss.Top, sidebar, diffPane)
	}
	return padBlockHeight(body, bodyHeight, m.width) + "\n" + m.renderStatus()
}

func (m *Model) reload(ctx context.Context) error {
	compareTarget, err := m.resolveCompareTarget(ctx)
	if err != nil {
		return err
	}
	s, err := snapshot.Load(ctx, m.repo, git.DiffOptions{Mode: m.cfg.Mode, Base: m.cfg.Base, IgnoreWhitespace: m.cfg.IgnoreWhitespace})
	if err != nil {
		return err
	}
	anchor := m.cursorAnchor()
	m.compareTarget = compareTarget
	m.session.SetSnapshot(s.Files, s.Hash)
	m.resetFileHashes(s.Files)
	m.restoreCursor(anchor)
	m.viewCache = make(map[string]string)
	m.statsCache = make(map[string]diffStats)
	m.syntaxCache = make(map[string]string)
	m.splitHunkCache = make(map[string]map[string]bool)
	m.splitNavCache = make(map[string]splitNav)
	m.splitOffset = 0
	return nil
}

type cursorAnchor struct {
	Path    string
	FileIdx int
	LineIdx int
	Side    thread.Side
	LineNo  int
}

func (m Model) cursorAnchor() cursorAnchor {
	anchor := cursorAnchor{Path: m.currentPath(), FileIdx: m.session.FileIndex(), LineIdx: m.session.LineIndex()}
	dl := m.session.SelectedLine()
	if dl.Line == nil {
		return anchor
	}
	side, _, ok := threadtarget.ForLine(*dl.Line)
	if !ok {
		return anchor
	}
	anchor.Side = side
	anchor.LineNo = threadtarget.LineNumber(*dl.Line, side)
	return anchor
}

func (m *Model) restoreCursor(anchor cursorAnchor) {
	files := m.session.Files()
	if len(files) == 0 {
		return
	}
	fileIdx := clamp(anchor.FileIdx, 0, len(files)-1)
	for i, f := range files {
		if f.Path() == anchor.Path {
			fileIdx = i
			break
		}
	}
	lineIdx := anchor.LineIdx
	if anchor.LineNo > 0 && anchor.Side != "" {
		lines := review.DisplayLinesForFile(files[fileIdx])
		for i, dl := range lines {
			if dl.Line != nil && threadtarget.LineNumber(*dl.Line, anchor.Side) == anchor.LineNo {
				lineIdx = i
				break
			}
		}
	}
	m.session.JumpToIndex(fileIdx, lineIdx, m.bodyHeight())
	m.ensureSplitCursorVisible(m.bodyHeight())
}

func (m *Model) resetFileHashes(files []diff.File) {
	m.fileHashes = fileHashes(files)
	if m.changedFiles == nil {
		m.changedFiles = make(map[string]bool)
	}
}

func (m *Model) updateChangedFiles(files []diff.File) {
	newHashes := fileHashes(files)
	if m.fileHashes == nil {
		m.fileHashes = newHashes
		return
	}
	if m.changedFiles == nil {
		m.changedFiles = make(map[string]bool)
	}
	for path, newHash := range newHashes {
		if oldHash, ok := m.fileHashes[path]; !ok || oldHash != newHash {
			m.changedFiles[path] = true
		}
	}
	for path := range m.changedFiles {
		if _, ok := newHashes[path]; !ok {
			delete(m.changedFiles, path)
		}
	}
	m.fileHashes = newHashes
}

func fileHashes(files []diff.File) map[string]string {
	hashes := make(map[string]string, len(files))
	for _, f := range files {
		hashes[f.Path()] = diff.FileHash(f)
	}
	return hashes
}

func (m *Model) acknowledgeCurrentFileChange() {
	m.acknowledgeFileChange(m.currentPath())
}

func (m *Model) acknowledgeFileChange(path string) {
	if m.changedFiles == nil || path == "" {
		return
	}
	delete(m.changedFiles, path)
}

func (m Model) changedFilesKey() string {
	if len(m.changedFiles) == 0 {
		return ""
	}
	var b strings.Builder
	for _, f := range m.session.Files() {
		if m.changedFiles[f.Path()] {
			b.WriteString(f.Path())
			b.WriteByte('\x00')
		}
	}
	return b.String()
}

func (m Model) resolveCompareTarget(ctx context.Context) (string, error) {
	return resolveCompareTarget(ctx, m.repo, m.cfg)
}

func resolveCompareTarget(ctx context.Context, repo git.Repo, cfg Config) (string, error) {
	switch cfg.Mode {
	case git.ModeStaged:
		return "HEAD", nil
	case git.ModeUnstaged:
		return "staged", nil
	}
	if cfg.Base != "" {
		return cfg.Base, nil
	}
	base, err := repo.DefaultBase(ctx)
	if err != nil {
		return "", err
	}
	if base == "" {
		return "none", nil
	}
	return base, nil
}

func (m Model) compareTargetLabel() string {
	if m.compareTarget != "" {
		return m.compareTarget
	}
	switch m.cfg.Mode {
	case git.ModeStaged:
		return "HEAD"
	case git.ModeUnstaged:
		return "staged"
	}
	if m.cfg.Base != "" {
		return m.cfg.Base
	}
	return "none"
}

func (m Model) shouldAutoRefresh() bool {
	return m.ready() && !m.cfg.Offline && !m.composing && !m.prPicker.Active() && !m.prAttaching && !m.refreshing && m.store != nil && m.store.GitHub != nil && m.store.GitHub.Number > 0
}

func (m Model) refreshProjectCmd(auto bool) tea.Cmd {
	repo := m.repo
	cfg := m.cfg
	var existingPR *gh.AttachedPR
	if m.store != nil && m.store.GitHub != nil {
		pr := *m.store.GitHub
		existingPR = &pr
	}
	return func() tea.Msg {
		ctx := context.Background()
		compareTarget, err := resolveCompareTarget(ctx, repo, cfg)
		if err != nil {
			return refreshLoadedMsg{err: err, auto: auto}
		}
		snap, err := snapshot.Load(ctx, repo, git.DiffOptions{Mode: cfg.Mode, Base: cfg.Base, IgnoreWhitespace: cfg.IgnoreWhitespace})
		if err != nil {
			return refreshLoadedMsg{err: err, auto: auto}
		}
		msg := refreshLoadedMsg{snap: snap, compareTarget: compareTarget, auto: auto}
		if cfg.Offline {
			msg.offline = true
			return msg
		}
		client := gh.NewClient(repo.Root)
		pr := existingPR
		if pr != nil && pr.Number > 0 {
			refreshed, err := client.PRView(ctx, pr.Number)
			if err == nil {
				pr = &refreshed
			}
		}
		if pr == nil || pr.Number == 0 {
			detected, err := client.AutoDetectPR(ctx)
			if err != nil {
				msg.noPR = true
				return msg
			}
			pr = &detected
		}
		threads, threadErr := client.Threads(ctx, *pr)
		msg.pr = pr
		msg.threads = threads
		msg.threadErr = threadErr
		return msg
	}
}

type displayLine = review.DisplayLine

func (m Model) currentLines() []displayLine {
	return m.session.CurrentLines()
}

func (m Model) selectedLine() displayLine {
	return m.session.SelectedLine()
}

func (m Model) selectedThread() (thread.Thread, bool) {
	return m.session.SelectedThread()
}

func (m *Model) markSelectedThreadRead() {
	if m.store == nil {
		return
	}
	selected, ok := m.selectedThread()
	if !ok || !thread.UnreadForHuman(selected) {
		return
	}
	if err := m.store.MarkThreadRead(selected.ID); err == nil {
		m.invalidateViewCache()
	}
}

func (m *Model) startEditThread(t thread.Thread) error {
	if !thread.CanEditLatestMessage(t) {
		return fmt.Errorf("can only edit latest local message")
	}
	m.composerBaseView = m.reviewView()
	m.editingThreadID = t.ID
	m.replyingThreadID = ""
	m.pendingTarget = threadworkflow.Target{}
	m.editor.Reset()
	m.editor.Placeholder = "edit latest reply…"
	m.editor.SetValue(thread.LastMessage(t).Body)
	m.composing = true
	m.editor.Focus()
	return nil
}

func (m *Model) startReplyThread(t thread.Thread) {
	m.composerBaseView = m.reviewView()
	m.editingThreadID = ""
	m.replyingThreadID = t.ID
	m.pendingTarget = threadworkflow.Target{}
	m.editor.Reset()
	m.editor.Placeholder = "reply…"
	m.composing = true
	m.editor.Focus()
}

func (m *Model) startNewThread(target threadworkflow.Target) {
	m.composerBaseView = m.reviewView()
	m.editingThreadID = ""
	m.replyingThreadID = ""
	m.pendingTarget = target
	m.editor.Reset()
	m.editor.Placeholder = "add review comment…"
	m.composing = true
	m.editor.Focus()
}

func (m Model) singleLineTarget() (threadworkflow.Target, error) {
	return m.threads.TargetForDisplayLine(m.selectedLine())
}

func (m Model) rangeTarget() (threadworkflow.Target, error) {
	if m.split {
		return m.threads.TargetForDisplayRangeSide(m.splitRangeLines(), m.session.RangeSide())
	}
	return m.threads.TargetForDisplayRange(m.session.RangeLines())
}

func (m Model) splitRangeLines() []review.DisplayLine {
	if !m.session.RangeActive() {
		return nil
	}
	side := m.session.RangeSide()
	if side == "" {
		return m.session.RangeLines()
	}
	lines := m.session.CurrentLines()
	rows := m.splitNavForCurrentFile().rows
	start, end := m.session.RangeIndexes()
	startRow := splitRowIndexForLine(rows, start)
	endRow := splitRowIndexForLine(rows, end)
	if startRow < 0 || endRow < 0 {
		return nil
	}
	if startRow > endRow {
		startRow, endRow = endRow, startRow
	}
	out := make([]review.DisplayLine, 0, endRow-startRow+1)
	for i := startRow; i <= endRow && i < len(rows); i++ {
		idx := splitRowLineForSide(rows[i], side)
		if idx >= 0 && idx < len(lines) {
			out = append(out, lines[idx])
		}
	}
	return out
}

func (m Model) currentPath() string {
	return m.session.CurrentPath()
}

type diffStats struct {
	Added   int
	Deleted int
}

func (s diffStats) String() string {
	return fmt.Sprintf("+%d -%d", s.Added, s.Deleted)
}

func computeFileStats(f diff.File) diffStats {
	var s diffStats
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			switch l.Kind {
			case diff.Add:
				s.Added++
			case diff.Delete:
				s.Deleted++
			}
		}
	}
	return s
}

func (m Model) fileStats(f diff.File) diffStats {
	path := f.Path()
	if m.statsCache != nil {
		if stats, ok := m.statsCache[path]; ok {
			return stats
		}
	}
	stats := computeFileStats(f)
	if m.statsCache != nil {
		m.statsCache[path] = stats
	}
	return stats
}

func (m Model) totalStats() diffStats {
	var total diffStats
	for _, f := range m.session.Files() {
		s := m.fileStats(f)
		total.Added += s.Added
		total.Deleted += s.Deleted
	}
	return total
}

func (m Model) statsView(s diffStats) string {
	var parts []string
	if s.Added > 0 {
		parts = append(parts, addStyle.Render(fmt.Sprintf("+%d", s.Added)))
	}
	if s.Deleted > 0 {
		parts = append(parts, deleteStyle.Render(fmt.Sprintf("-%d", s.Deleted)))
	}
	return strings.Join(parts, " ")
}

func (m Model) sidebarStatsView(s diffStats) string {
	var parts []string
	if add := sidebarStat("+", s.Added); add != "" {
		parts = append(parts, addStyle.Render(add))
	}
	if del := sidebarStat("-", s.Deleted); del != "" {
		parts = append(parts, deleteStyle.Render(del))
	}
	return strings.Join(parts, " ")
}

func sidebarStat(prefix string, count int) string {
	if count == 0 {
		return ""
	}
	if count >= 1000 {
		return fmt.Sprintf("%s%dk", prefix, count/1000)
	}
	return fmt.Sprintf("%s%d", prefix, count)
}

func (m Model) threadCount(path string) int {
	return m.session.ThreadCount(path)
}

func (m Model) unreadThreadCount(path string) int {
	if m.store == nil {
		return 0
	}
	unread := 0
	for _, t := range m.store.ThreadsFor(path) {
		if thread.UnreadForHuman(t) {
			unread++
		}
	}
	return unread
}

func (m Model) totalThreadCount() int {
	total := 0
	for _, f := range m.session.Files() {
		total += m.threadCount(f.Path())
	}
	return total
}

func (m Model) totalUnreadThreadCount() int {
	total := 0
	for _, f := range m.session.Files() {
		total += m.unreadThreadCount(f.Path())
	}
	return total
}

func (m Model) threadsView(count int) string {
	return threadBadgeView(count, m.totalUnreadThreadCount())
}

func (m Model) threadBadgeForPath(path string) string {
	return threadBadgeText(m.threadCount(path), m.unreadThreadCount(path))
}

func threadBadgeText(count, unread int) string {
	if count == 0 {
		return ""
	}
	if unread > 0 {
		return fmt.Sprintf("●%d", unread)
	}
	return fmt.Sprintf("○%d", count)
}

func threadBadgeView(count, unread int) string {
	text := threadBadgeText(count, unread)
	if text == "" {
		return ""
	}
	return threadStyle.Render(text)
}

func sidebarHeader(stats, threads string) string {
	parts := []string{titleStyle.Render("tdiff")}
	if stats != "" {
		parts = append(parts, stats)
	}
	if threads != "" {
		parts = append(parts, threads)
	}
	return strings.Join(parts, " ")
}

func selectedSidebarLine(prefix, viewed, changed string, nameW int, path, added, deleted, threadBadge string, threadW int) string {
	rail := selectedStyle.Render(prefix)
	if strings.Contains(prefix, "▌") {
		rail = selectedThreadStyle.Render("▌") + selectedStyle.Render(" ")
	}
	line := rail +
		selectedStyle.Render(viewed+" ") +
		selectedThreadStyle.Render(changed+" ") +
		sidebarPath(path, nameW, true, viewed == "✓") +
		selectedStyle.Render(" ") +
		selectedAddStyle.Render(added) +
		selectedStyle.Render(" ") +
		selectedDeleteStyle.Render(deleted) +
		selectedStyle.Render(" ") +
		selectedSidebarThreadView(threadBadge, threadW)
	return padRightStyled(line, sidebarWidth, selectedStyle)
}

func sidebarThreadView(badge string, width int) string {
	thread := strings.Repeat(" ", width)
	if badge != "" {
		thread = fmt.Sprintf("%*s", width, badge)
	}
	if badge != "" {
		return threadStyle.Render(thread)
	}
	return dimStyle.Render(thread)
}

func selectedSidebarThreadView(badge string, width int) string {
	thread := sidebarThreadView(badge, width)
	if badge == "" {
		return selectedStyle.Render(thread)
	}
	return selectedThreadStyle.Render(thread)
}

func sidebarColumnWidths(files []diff.File, threadBadge func(string) string, statsFor func(diff.File) diffStats) (int, int, int, int) {
	addW, delW, threadW := 0, 0, 2
	for _, f := range files {
		stats := statsFor(f)
		addW = max(addW, xansi.StringWidth(sidebarStat("+", stats.Added)))
		delW = max(delW, xansi.StringWidth(sidebarStat("-", stats.Deleted)))
		if badge := threadBadge(f.Path()); badge != "" {
			threadW = max(threadW, xansi.StringWidth(badge))
		}
	}
	nameW := max(8, sidebarWidth-9-addW-delW-threadW)
	return nameW, addW, delW, threadW
}

func sidebarPath(path string, width int, selected, viewed bool) string {
	name := compactPath(path, width)
	prefix, base := "", name
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		prefix = name[:idx+1]
		base = name[idx+1:]
	}
	pad := strings.Repeat(" ", max(0, width-xansi.StringWidth(name)))
	if selected {
		if viewed {
			return selectedDimStyle.Render(prefix + base + pad)
		}
		return selectedDimStyle.Render(prefix) + selectedStyle.Render(base+pad)
	}
	if viewed {
		return dimStyle.Render(prefix + base + pad)
	}
	return dimStyle.Render(prefix) + base + pad
}

func (m *Model) openPRPicker() tea.Cmd {
	if m.cfg.Offline {
		m.status = "offline mode"
		return nil
	}
	m.prPicker.Open()
	return tea.Batch(loadPRListCmd(m.repo.Root), loadingSpinnerTick())
}

func loadPRListCmd(root string) tea.Cmd {
	return func() tea.Msg {
		prs, err := gh.NewClient(root).PRList(context.Background())
		return prListLoadedMsg{prs: prs, err: err}
	}
}

func (m Model) updatePRPicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	previousStatus := m.status
	number, attach, err := m.prPicker.UpdateKey(msg)
	if err != nil {
		m.status = err.Error()
		return m, m.statusToastCmd(previousStatus)
	}
	if !attach {
		return m, nil
	}
	if m.store != nil && m.store.GitHub != nil && m.store.GitHub.Number == number {
		m.status = fmt.Sprintf("PR #%d already attached", number)
		return m, m.statusToastCmd(previousStatus)
	}
	m.prAttaching = true
	m.status = fmt.Sprintf("attaching PR #%d…", number)
	return m, tea.Batch(attachPRCmd(m.repo.Root, number), loadingSpinnerTick())
}

func attachPRCmd(root string, number int) tea.Cmd {
	return func() tea.Msg {
		client := gh.NewClient(root)
		pr, err := client.PRView(context.Background(), number)
		if err != nil {
			return prAttachLoadedMsg{err: err}
		}
		threads, threadErr := client.Threads(context.Background(), pr)
		return prAttachLoadedMsg{pr: pr, threads: threads, threadErr: threadErr}
	}
}

func (m Model) updateJumpPrompt(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	previousStatus := m.status
	switch msg.String() {
	case "esc":
		m.jumpPrompt = false
		m.jumpInput = ""
		return m, nil
	case "enter":
		line, err := strconv.Atoi(m.jumpInput)
		m.jumpPrompt = false
		m.jumpInput = ""
		if err != nil || line <= 0 {
			m.status = "invalid line"
			return m, m.statusToastCmd(previousStatus)
		}
		if !m.jumpToFileLine(line) {
			m.status = fmt.Sprintf("line %d not in diff", line)
		}
		return m, m.statusToastCmd(previousStatus)
	case "backspace":
		if len(m.jumpInput) > 0 {
			m.jumpInput = m.jumpInput[:len(m.jumpInput)-1]
		}
		return m, nil
	}
	for _, r := range msg.Runes {
		if r >= '0' && r <= '9' {
			m.jumpInput += string(r)
		}
	}
	return m, nil
}

func (m *Model) statusToastCmd(previous string) tea.Cmd {
	if m.status == "" || m.status == previous {
		return nil
	}
	m.statusID++
	id := m.statusID
	return tea.Tick(statusToastDuration, func(time.Time) tea.Msg {
		return clearStatusMsg{id: id}
	})
}

func (m *Model) jumpTop() {
	m.session.JumpTop()
	m.splitOffset = 0
}

func (m *Model) jumpBottom() {
	m.session.JumpBottom(m.bodyHeight())
	m.ensureSplitCursorVisible(m.bodyHeight())
}

func (m *Model) nextHunk() {
	if !m.session.NextHunk(m.bodyHeight()) {
		m.status = "no next hunk"
	}
	m.ensureSplitCursorVisible(m.bodyHeight())
}

func (m *Model) prevHunk() {
	if !m.session.PrevHunk(m.bodyHeight()) {
		m.status = "no previous hunk"
	}
	m.ensureSplitCursorVisible(m.bodyHeight())
}

func (m *Model) nextThread() {
	m.jumpThread(1)
}

func (m *Model) prevThread() {
	m.jumpThread(-1)
}

func (m *Model) jumpThread(delta int) {
	before := m.currentPath()
	idx, total, ok := m.session.JumpThread(delta, m.bodyHeight())
	if !ok {
		m.status = "no threads"
		return
	}
	if m.currentPath() != before {
		m.acknowledgeCurrentFileChange()
		m.invalidateViewCache()
	}
	m.status = fmt.Sprintf("thread %d/%d", idx, total)
}

func (m *Model) jumpToFileLine(line int) bool {
	ok := m.session.JumpToFileLine(line, m.bodyHeight())
	m.ensureSplitCursorVisible(m.bodyHeight())
	return ok
}

func (m *Model) ensureCursorVisible() {
	m.session.EnsureVisible(m.bodyHeight())
	m.ensureSplitCursorVisible(m.bodyHeight())
}

func (m Model) bodyHeight() int {
	if m.height == 0 {
		return 29
	}
	return max(1, m.height-m.footerHeight())
}

func (m Model) footerHeight() int {
	return 1
}

func (m *Model) invalidateViewCache() {
	m.viewCache = make(map[string]string)
}

func (m Model) sidebarCacheKey(height int) string {
	selectedID := ""
	if selected, ok := m.selectedThread(); ok {
		selectedID = selected.ID
	}
	return fmt.Sprintf("sidebar:%d:%d:%d:%s:%d:%d:%t:%t:%s", height, m.session.FileIndex(), len(m.session.Files()), selectedID, m.totalThreadCount(), m.totalUnreadThreadCount(), m.session.HideViewed(), m.session.ThreadsOnly(), m.changedFilesKey())
}

func (m Model) renderSidebar(height int) string {
	cacheKey := m.sidebarCacheKey(height)
	if m.viewCache != nil {
		if cached, ok := m.viewCache[cacheKey]; ok {
			return cached
		}
	}
	style := lipgloss.NewStyle().Width(sidebarWidth)
	files := m.session.Files()
	fileIdx := m.session.FileIndex()
	nameW, addW, delW, threadW := sidebarColumnWidths(files, m.threadBadgeForPath, m.fileStats)
	previewHeight := sidebarThreadHeight(height, m.totalThreadCount())
	fileHeight := height - previewHeight
	var rows []string
	rows = append(rows, sidebarHeader(m.sidebarStatsView(m.totalStats()), m.threadsView(m.totalThreadCount())))
	start := clamp(fileIdx-fileHeight/2, 0, max(0, len(files)-fileHeight+1))
	end := min(len(files), start+fileHeight-1)
	for i := start; i < end; i++ {
		f := files[i]
		path := f.Path()
		prefix := "  "
		if i == fileIdx {
			prefix = "▌ "
		}
		viewed := " "
		if m.session.IsViewed(path) {
			viewed = "✓"
		}
		changed := " "
		if m.changedFiles[path] {
			changed = "◆"
		}
		stats := m.fileStats(f)
		threadCount := m.threadCount(path)
		threadBadge := threadBadgeText(threadCount, m.unreadThreadCount(path))
		addedText := fmt.Sprintf("%*s", addW, sidebarStat("+", stats.Added))
		deletedText := fmt.Sprintf("%*s", delW, sidebarStat("-", stats.Deleted))
		added := addStyle.Render(addedText)
		deleted := deleteStyle.Render(deletedText)
		line := fmt.Sprintf("%s%s %s %s %s %s %s", prefix, viewed, threadStyle.Render(changed), sidebarPath(path, nameW, false, viewed == "✓"), added, deleted, sidebarThreadView(threadBadge, threadW))
		if i == fileIdx {
			line = selectedSidebarLine(prefix, viewed, changed, nameW, path, addedText, deletedText, threadBadge, threadW)
		} else if viewed == "✓" {
			line = dimStyle.Render(line)
		}
		rows = append(rows, line)
	}
	if previewHeight > 0 {
		rows = append(rows, m.renderThreadPreview(previewHeight)...)
	}
	out := style.Render(strings.Join(rows, "\n"))
	if m.viewCache != nil {
		if len(m.viewCache) > 32 {
			clear(m.viewCache)
		}
		m.viewCache[cacheKey] = out
	}
	return out
}

func sidebarThreadHeight(height, threadCount int) int {
	if threadCount == 0 || height < sidebarMinFileRows+sidebarMinThreadRows {
		return 0
	}
	available := height - sidebarMinFileRows
	if available < sidebarMinThreadRows {
		return 0
	}
	desired := sidebarThreadHeaderRows + threadCount*sidebarRowsPerThread
	desired = max(sidebarMinThreadRows, desired)
	maxPreview := min(sidebarThreadMaxRows, height/2)
	return min(maxPreview, min(available, desired))
}

func (m Model) threadPositions() []review.ThreadPosition {
	return m.session.ThreadPositions()
}
func (m Model) renderThreadPreview(maxRows int) []string {
	positions := m.threadPositions()
	if len(positions) == 0 || maxRows <= 1 {
		return nil
	}
	selected, hasSelected := m.selectedThread()
	rows := []string{dimStyle.Render(""), titleStyle.Render("threads")}
	remaining := maxRows - len(rows)
	maxItems := max(1, remaining/2)
	start := 0
	if hasSelected {
		for i, p := range positions {
			if p.Thread.ID == selected.ID {
				start = clamp(i-maxItems/2, 0, max(0, len(positions)-maxItems))
				break
			}
		}
	}
	for i := start; i < len(positions) && remaining > 0; i++ {
		p := positions[i]
		replies := threadReplyCount(p.Thread)
		replySuffix := ""
		if replies > 0 {
			replySuffix = fmt.Sprintf(" ↳%d", replies)
		}
		glyph := threadGlyph(p.Thread)
		loc := fmt.Sprintf("%s %s:%d%s", glyph, compactPath(p.Thread.Path, sidebarWidth-8-xansi.StringWidth(replySuffix)), p.Thread.LineStart, replySuffix)
		if p.Thread.LineEnd != 0 && p.Thread.LineEnd != p.Thread.LineStart {
			loc = fmt.Sprintf("%s %s:%d-%d%s", glyph, compactPath(p.Thread.Path, sidebarWidth-10-xansi.StringWidth(replySuffix)), p.Thread.LineStart, p.Thread.LineEnd, replySuffix)
		}
		bodyText := strings.ReplaceAll(thread.Body(p.Thread), "\n", " ")
		if author := threadAuthor(p.Thread); author != "" {
			bodyText = author + ": " + bodyText
		}
		body := "  " + truncate(bodyText, sidebarWidth-2)
		isSelected := hasSelected && selected.ID == p.Thread.ID
		if isSelected {
			rows = append(rows, padRightStyled(selectedThreadStyle.Render(truncate(loc, sidebarWidth)), sidebarWidth, selectedStyle))
		} else {
			rows = append(rows, threadStyle.Render(truncate(loc, sidebarWidth)))
		}
		remaining--
		if remaining <= 0 {
			break
		}
		if isSelected {
			rows = append(rows, padRightStyled(selectedStyle.Render(body), sidebarWidth, selectedStyle))
		} else {
			rows = append(rows, dimStyle.Render(body))
		}
		remaining--
	}
	return rows
}

func (m Model) renderDiffHeader(width int) string {
	path := compactPath(m.currentPath(), max(12, width-18))
	stats := m.statsView(m.fileStats(m.session.Files()[m.session.FileIndex()]))
	if threads := m.threadCount(m.currentPath()); threads > 0 {
		stats = strings.TrimSpace(stats + " " + threadBadgeView(threads, m.unreadThreadCount(m.currentPath())))
	}
	line := titleStyle.Render(path)
	if stats != "" {
		line += "  " + stats
	}
	return padRight(truncate(line, width), width)
}

func (m Model) renderStatus() string {
	compareTarget := m.compareTargetLabel()
	files := m.session.Files()
	stats := m.statsView(m.totalStats())
	if threads := m.totalThreadCount(); threads > 0 {
		stats = strings.TrimSpace(stats + " " + threadBadgeView(threads, m.totalUnreadThreadCount()))
	}
	parts := []string{dimStyle.Render(compareTarget), dimStyle.Render(fmt.Sprintf("%d/%d", min(m.session.FileIndex()+1, len(files)), len(files)))}
	if stats != "" {
		parts = append(parts, stats)
	}
	if m.store != nil && m.store.ReviewStatus(m.session.DiffHash()) == "approved" {
		parts = append(parts, successStyle.Render("approved"))
	}
	if m.cfg.Offline {
		parts = append(parts, dimStyle.Render("offline"))
	}
	if m.store != nil && m.store.GitHub != nil && m.store.GitHub.Number > 0 {
		parts = append(parts, prStatusView(*m.store.GitHub))
	}
	if m.split {
		parts = append(parts, dimStyle.Render("split"))
	}
	if m.cfg.IgnoreWhitespace {
		parts = append(parts, dimStyle.Render("ignore-space"))
	}
	if m.session.HideViewed() {
		parts = append(parts, dimStyle.Render("hide-viewed"))
	}
	if m.session.ThreadsOnly() {
		parts = append(parts, dimStyle.Render("threads-only"))
	}
	if m.hideSidebar {
		parts = append(parts, dimStyle.Render("sidebar-off"))
	}
	if !m.syntax {
		parts = append(parts, dimStyle.Render("syntax-off"))
	} else if !m.syntaxAllowed(m.session.CurrentLineCount()) {
		parts = append(parts, dimStyle.Render("syntax-skipped"))
	}
	if !m.contextDim {
		parts = append(parts, dimStyle.Render("context-dim-off"))
	}
	if m.refreshing {
		frame := loadingSpinnerFrames[m.loadingFrame%len(loadingSpinnerFrames)]
		parts = append(parts, dimStyle.Render(frame+" refreshing"))
	}
	if m.session.RangeActive() {
		start, end := m.session.RangeIndexes()
		parts = append(parts, threadStyle.Render(fmt.Sprintf("range %d–%d", start+1, end+1)))
	}
	if m.status != "" {
		parts = append(parts, statusView(m.status))
	}
	left := joinDim(parts, " · ")
	right := m.footerHints()
	gap := max(1, m.width-xansi.StringWidth(left)-xansi.StringWidth(right)-1)
	tailStyle := dimStyle
	if m.session.RangeActive() {
		tailStyle = rangeFooterStyle
	}
	line := left + strings.Repeat(" ", gap) + tailStyle.Render(right)
	out := truncate(line, m.width-1)
	if m.session.RangeActive() {
		return rangeFooterStyle.Render(out)
	}
	return out
}

func joinDim(parts []string, sep string) string {
	return strings.Join(parts, dimStyle.Render(sep))
}

func prStatusView(pr gh.AttachedPR) string {
	label := fmt.Sprintf("PR #%d", pr.Number)
	switch pr.Status {
	case gh.PRStatusReady:
		return successStyle.Render(label + " ready")
	case gh.PRStatusDraft:
		return dimStyle.Render(label + " draft")
	case gh.PRStatusBehind:
		return blueStyle.Render(label + " behind")
	case gh.PRStatusBlocked:
		return errorStyle.Render(label + " blocked")
	case gh.PRStatusMerged:
		return purpleStyle.Render(label + " merged")
	case gh.PRStatusClosed:
		return dimStyle.Render(label + " closed")
	default:
		return dimStyle.Render(label)
	}
}

func statusView(s string) string {
	lower := strings.ToLower(s)
	if strings.Contains(lower, "invalid") || strings.Contains(lower, "error") || strings.Contains(lower, "failed") || strings.Contains(lower, "unsupported") || strings.Contains(lower, "empty") || strings.Contains(lower, "cannot") || strings.HasPrefix(lower, "no ") || strings.Contains(lower, " must ") {
		return errorStyle.Render(s)
	}
	if strings.Contains(lower, "saved") || strings.Contains(lower, "copied") || strings.Contains(lower, "marked") || strings.Contains(lower, "deleted") {
		return successStyle.Render(s)
	}
	return dimStyle.Render(s)
}

func (m Model) footerHints() string {
	if m.prPicker.Active() {
		return "# attach PR · enter attach · esc cancel"
	}
	if m.jumpPrompt {
		return ":" + m.jumpInput + "  enter jump · esc cancel"
	}
	if m.session.RangeActive() {
		return "j/k extend · a add thread · r cancel"
	}
	if m.showHelp {
		return "? close"
	}
	if _, ok := m.selectedThread(); ok {
		return "z resolve · enter reply · e edit · d delete · ]t/[t threads · y copy"
	}
	return "a add thread · A approve · r range · v viewed · ? help"
}

func (m Model) renderHelp() string {
	lines := []string{
		"nav",
		"  j/k        line up/down",
		"  gg/G       top/bottom",
		"  ]h/[h      next/previous hunk",
		"  ]t/[t      next/previous thread",
		"  :line      jump to file line",
		"  n/p        next/previous file",
		"  #          attach/change PR",
		"",
		"review",
		"  v          toggle viewed",
		"  A          approve review",
		"  u          hide/show viewed files",
		"  m          threads-only filter",
		"  r          start/cancel range",
		"  a/e/d      add/edit/delete thread",
		"  z          resolve/reopen selected thread",
		"  enter      reply to selected thread",
		"  y/Y        copy selected/all threads",
		"  ⌥+enter    save thread",
		"  esc        cancel thread",
		"",
		"view",
		"  s          split/unified",
		"  b          show/hide sidebar",
		"  i          inline threads",
		"  x          syntax highlighting",
		"  c          context dimming",
		"  w          wrap cursor line",
		"  L          line numbers",
		"  W          whitespace",
		"  R          refresh diff",
		"  ?          close help",
	}
	boxWidth := min(56, max(36, m.width-6))
	return helpBox("help", lines, boxWidth)
}

func helpBox(title string, lines []string, width int) string {
	contentWidth := max(1, width-4)
	titleText := " " + title + " "
	topFill := max(0, width-2-xansi.StringWidth(titleText))
	rows := []string{helpBorderStyle.Render("┌" + titleText + strings.Repeat("─", topFill) + "┐")}
	for _, line := range lines {
		rows = append(rows, helpBorderStyle.Render("│ ")+helpBgStyle.Render(padRight(truncate(line, contentWidth), contentWidth))+helpBorderStyle.Render(" │"))
	}
	rows = append(rows, helpBorderStyle.Render("└"+strings.Repeat("─", max(0, width-2))+"┘"))
	return strings.Join(rows, "\n")
}

func (m *Model) saveThread() error {
	if m.replyingThreadID != "" {
		body := strings.TrimSpace(m.editor.Value())
		if body == "" {
			return fmt.Errorf("empty thread")
		}
		return m.store.Reply(m.replyingThreadID, thread.Message{Actor: thread.ActorHuman, Body: body})
	}
	target := m.pendingTarget
	if m.editingThreadID == "" && target.LineStart == 0 {
		var err error
		target, err = m.singleLineTarget()
		if err != nil {
			return err
		}
	}
	return m.threads.Save(m.currentPath(), m.session.DiffHash(), m.editingThreadID, target, m.editor.Value())
}

const (
	sidebarWidth            = 38
	sidebarMinFileRows      = 8
	sidebarMinThreadRows    = 6
	sidebarThreadMaxRows    = 24
	sidebarThreadHeaderRows = 2
	sidebarRowsPerThread    = 2
	lineNoWidth             = 4
	intralineContextLines   = 200
	syntaxMaxFileLines      = 2500
	splitSyntaxMaxFileLines = 600
	syntaxMaxLineWidth      = 500
	syntaxCacheMaxEntries   = 4000
	statusToastDuration     = 3 * time.Second
)

var (
	loadingSpinnerFrames    = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	brandColor              = lipgloss.Color("180")
	selectedBg              = lipgloss.Color("236")
	addChangedBg            = lipgloss.Color("22")
	deleteChangedBg         = lipgloss.Color("52")
	threadStyle             = lipgloss.NewStyle().Foreground(brandColor)
	selectedThreadStyle     = lipgloss.NewStyle().Foreground(brandColor).Background(selectedBg)
	titleStyle              = lipgloss.NewStyle().Bold(true).Foreground(brandColor)
	selectedStyle           = lipgloss.NewStyle().Background(selectedBg)
	rangeBg                 = lipgloss.Color("235")
	rangeStyle              = lipgloss.NewStyle().Background(rangeBg)
	rangeThreadStyle        = lipgloss.NewStyle().Foreground(brandColor).Background(rangeBg)
	rangeFooterStyle        = lipgloss.NewStyle().Foreground(brandColor)
	dimStyle                = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	markdownInlineCodeStyle = lipgloss.NewStyle().Foreground(brandColor).Background(lipgloss.Color("235"))
	markdownStrongStyle     = lipgloss.NewStyle().Bold(true)
	markdownLinkStyle       = lipgloss.NewStyle().Underline(true).Foreground(brandColor)
	markdownQuoteStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	markdownCodeBlockStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Background(lipgloss.Color("235"))
	rangeDimStyle           = lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Background(rangeBg)
	selectedDimStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Background(selectedBg)
	successStyle            = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	warningStyle            = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	blueStyle               = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	errorStyle              = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	purpleStyle             = lipgloss.NewStyle().Foreground(lipgloss.Color("141"))
	hunkColor               = lipgloss.Color("99")
	hunkStyle               = lipgloss.NewStyle().Foreground(hunkColor)
	selectedHunkStyle       = lipgloss.NewStyle().Foreground(hunkColor).Background(selectedBg)
	rangeHunkStyle          = lipgloss.NewStyle().Foreground(hunkColor).Background(rangeBg)
	addStyle                = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	selectedAddStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Background(selectedBg)
	rangeAddStyle           = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Background(rangeBg)
	deleteStyle             = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	selectedDeleteStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Background(selectedBg)
	rangeDeleteStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Background(rangeBg)
	helpStyle               = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(brandColor).Background(lipgloss.Color("235")).Padding(1, 2)
	helpBorderStyle         = lipgloss.NewStyle().Foreground(brandColor).Background(lipgloss.Color("235"))
	helpBgStyle             = lipgloss.NewStyle().Background(lipgloss.Color("235"))
)

func colorLine(kind diff.Kind, s string) string {
	switch kind {
	case diff.Add:
		return addStyle.Render(s)
	case diff.Delete:
		return deleteStyle.Render(s)
	default:
		return s
	}
}

func selectedColorLine(kind diff.Kind, s string) string {
	switch kind {
	case diff.Add:
		return selectedAddStyle.Render(s)
	case diff.Delete:
		return selectedDeleteStyle.Render(s)
	default:
		return selectedStyle.Render(s)
	}
}

func rangeColorLine(kind diff.Kind, s string) string {
	switch kind {
	case diff.Add:
		return rangeAddStyle.Render(s)
	case diff.Delete:
		return rangeDeleteStyle.Render(s)
	default:
		return rangeStyle.Render(s)
	}
}

func overlay(base, modal string, width, height int) string {
	if width <= 0 || height <= 0 {
		return modal
	}
	baseLines := strings.Split(base, "\n")
	for len(baseLines) < height {
		baseLines = append(baseLines, "")
	}
	if len(baseLines) > height {
		baseLines = baseLines[:height]
	}

	modalLines := strings.Split(modal, "\n")
	modalW := 0
	for _, line := range modalLines {
		modalW = max(modalW, xansi.StringWidth(line))
	}
	left := max(0, (width-modalW)/2)
	top := max(0, (height-len(modalLines))/2)
	for i, line := range modalLines {
		row := top + i
		if row >= len(baseLines) {
			break
		}
		baseLines[row] = padRight(strings.Repeat(" ", left)+padRight(line, modalW), width)
	}
	return strings.Join(baseLines, "\n")
}

func padBlockHeight(s string, height, width int) string {
	lines := strings.Split(s, "\n")
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", max(0, width)))
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}

func padRight(s string, width int) string {
	if width <= 0 {
		return ""
	}
	w := xansi.StringWidth(s)
	if w >= width {
		return truncate(s, width)
	}
	return s + strings.Repeat(" ", width-w)
}

func padRightStyled(s string, width int, style lipgloss.Style) string {
	if width <= 0 {
		return ""
	}
	w := xansi.StringWidth(s)
	if w >= width {
		return truncate(s, width)
	}
	return s + style.Render(strings.Repeat(" ", width-w))
}

func threadAuthor(n thread.Thread) string {
	msg := thread.FirstMessage(n)
	if msg.AuthorName != "" {
		return msg.AuthorName
	}
	if msg.AuthorLogin != "" {
		return msg.AuthorLogin
	}
	if n.GitHub == nil {
		return ""
	}
	if n.GitHub.AuthorName != "" {
		return n.GitHub.AuthorName
	}
	return n.GitHub.AuthorLogin
}

func threadText(n thread.Thread) string {
	loc := fmt.Sprintf("%s:%d", n.Path, n.LineStart)
	if n.LineEnd != 0 && n.LineEnd != n.LineStart {
		loc = fmt.Sprintf("%s:%d-%d", n.Path, n.LineStart, n.LineEnd)
	}
	body := thread.Body(n)
	if author := threadAuthor(n); author != "" {
		body = author + ": " + body
	}
	out := fmt.Sprintf("- [ ] `%s` (%s) %s", loc, n.Side, body)
	for _, r := range n.Messages[1:] {
		author := r.AuthorLogin
		if r.AuthorName != "" {
			author = r.AuthorName
		}
		if author == "" && r.Actor != "" {
			author = string(r.Actor)
		}
		if author != "" {
			out += fmt.Sprintf("\n  - %s: %s", author, r.Body)
		} else {
			out += fmt.Sprintf("\n  - %s", r.Body)
		}
	}
	if n.Context != "" {
		out += fmt.Sprintf("\n\n```diff\n%s\n```", n.Context)
	}
	return out
}

func compactPath(path string, width int) string {
	if width <= 0 {
		return ""
	}
	if xansi.StringWidth(path) <= width {
		return path
	}
	parts := strings.Split(path, "/")
	if len(parts) == 1 {
		return truncate(path, width)
	}
	base := parts[len(parts)-1]
	if xansi.StringWidth(base)+2 >= width {
		return "…/" + truncate(base, max(1, width-2))
	}
	dir := strings.Join(parts[:len(parts)-1], "/")
	prefixW := width - xansi.StringWidth(base) - 3
	if prefixW <= 0 {
		return "…/" + truncate(base, max(1, width-2))
	}
	return truncate(dir, prefixW) + "/…/" + base
}

func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	s = strings.ReplaceAll(s, "\t", "    ")
	s = strings.ReplaceAll(s, "\r", "")
	return xansi.Truncate(s, n, "…")
}

func clamp(v, low, high int) int { return min(max(v, low), high) }
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
