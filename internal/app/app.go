package app

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/owenps/tdiff/internal/annotate"
	"github.com/owenps/tdiff/internal/annotations"
	"github.com/owenps/tdiff/internal/diff"
	"github.com/owenps/tdiff/internal/git"
	gh "github.com/owenps/tdiff/internal/github"
	"github.com/owenps/tdiff/internal/review"
	"github.com/owenps/tdiff/internal/snapshot"
)

type Config struct {
	Base             string
	Mode             git.Mode
	IgnoreWhitespace bool
	Offline          bool
}

type Model struct {
	repo          git.Repo
	cfg           Config
	compareTarget string
	store         *annotate.Store
	annotations   annotations.Workflow

	session review.Session

	width  int
	height int

	loadingFrame        int
	pendingKey          string
	jumpPrompt          bool
	jumpInput           string
	split               bool
	syntax              bool
	contextDim          bool
	wrapCursorLine      bool
	showHelp            bool
	hideSidebar         bool
	composing           bool
	editingAnnotationID string
	pendingTarget       annotations.Target
	editor              textarea.Model
	prPicker            prPicker
	prAttaching         bool
	refreshing          bool
	status              string
	statusID            int
	composerBaseView    string
	syntaxCache         map[string]string
}

func New(ctx context.Context, cfg Config) (Model, error) {
	repo, err := git.Open(ctx)
	if err != nil {
		return Model{}, err
	}
	store, err := annotate.Open(repo.Root)
	if err != nil {
		return Model{}, err
	}
	m := Model{repo: repo, cfg: cfg, store: store, annotations: annotations.NewWorkflow(store), session: review.NewSession(nil), syntax: true, contextDim: true, syntaxCache: make(map[string]string)}
	m.session.SetFilterSources(store, m.annotationCount)
	m.session.SetAnnotationSources(store.AnnotationsFor, m.annotations.AnnotationAt)
	m.editor = textarea.New()
	m.editor.Placeholder = "annotation"
	m.editor.CharLimit = 4000
	m.editor.SetHeight(5)
	m.editor.ShowLineNumbers = false
	if err := m.reload(ctx); err != nil {
		return Model{}, err
	}
	return m, nil
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(textarea.Blink, loadingSpinnerTick())
}

type clearStatusMsg struct{ id int }
type loadingSpinnerTickMsg struct{}
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
}

func loadingSpinnerTick() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg {
		return loadingSpinnerTickMsg{}
	})
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(loadingSpinnerTickMsg); ok {
		if !m.ready() || m.prPicker.Loading() || m.prAttaching || m.refreshing {
			m.loadingFrame++
			return m, loadingSpinnerTick()
		}
		return m, nil
	}
	if msg, ok := msg.(prListLoadedMsg); ok {
		if !m.prPicker.Active() {
			return m, nil
		}
		attachedNumber := 0
		if m.store != nil && m.store.GitHub != nil {
			attachedNumber = m.store.GitHub.Number
		}
		m.prPicker.SetLoaded(msg.prs, msg.err, attachedNumber)
		return m, nil
	}
	if msg, ok := msg.(prAttachLoadedMsg); ok {
		previousStatus := m.status
		m.prAttaching = false
		if msg.err != nil {
			m.status = msg.err.Error()
			return m, m.statusToastCmd(previousStatus)
		}
		if err := m.store.AttachGitHubPR(msg.pr); err != nil {
			m.status = err.Error()
			return m, m.statusToastCmd(previousStatus)
		}
		if msg.threadErr != nil {
			m.status = fmt.Sprintf("attached PR #%d · sync failed", msg.pr.Number)
			return m, m.statusToastCmd(previousStatus)
		}
		count, err := m.store.SyncGitHubAnnotations(msg.pr, msg.threads)
		if err != nil {
			m.status = err.Error()
			return m, m.statusToastCmd(previousStatus)
		}
		m.session.RefreshFilters()
		m.status = fmt.Sprintf("attached PR #%d · %d annotations", msg.pr.Number, count)
		return m, m.statusToastCmd(previousStatus)
	}
	if msg, ok := msg.(refreshLoadedMsg); ok {
		previousStatus := m.status
		m.refreshing = false
		if msg.err != nil {
			m.status = msg.err.Error()
			return m, m.statusToastCmd(previousStatus)
		}
		m.compareTarget = msg.compareTarget
		m.session.SetSnapshot(msg.snap.Files, msg.snap.Hash)
		m.syntaxCache = make(map[string]string)
		if msg.offline {
			m.status = "diff refreshed · offline"
			return m, m.statusToastCmd(previousStatus)
		}
		if msg.noPR || msg.pr == nil {
			m.status = "diff refreshed · no PR"
			return m, m.statusToastCmd(previousStatus)
		}
		if err := m.store.AttachGitHubPR(*msg.pr); err != nil {
			m.status = err.Error()
			return m, m.statusToastCmd(previousStatus)
		}
		if msg.threadErr != nil {
			m.status = "diff refreshed · github sync failed"
			return m, m.statusToastCmd(previousStatus)
		}
		count, err := m.store.SyncGitHubAnnotations(*msg.pr, msg.threads)
		if err != nil {
			m.status = err.Error()
			return m, m.statusToastCmd(previousStatus)
		}
		m.session.RefreshFilters()
		m.status = fmt.Sprintf("PR #%d synced · %d annotations", msg.pr.Number, count)
		return m, m.statusToastCmd(previousStatus)
	}
	if msg, ok := msg.(clearStatusMsg); ok {
		if msg.id == m.statusID {
			m.status = ""
		}
		return m, nil
	}

	previousStatus := m.status
	if m.composing {
		switch msg := msg.(type) {
		case tea.KeyMsg:
			switch msg.String() {
			case "alt+enter":
				if err := m.saveAnnotation(); err != nil {
					m.status = err.Error()
				} else {
					m.status = "annotation saved"
					m.composing = false
					m.composerBaseView = ""
					m.session.CancelRange()
					m.editingAnnotationID = ""
					m.pendingTarget = annotations.Target{}
					m.editor.Blur()
					m.editor.Reset()
				}
				return m, m.statusToastCmd(previousStatus)
			case "esc":
				m.composing = false
				m.composerBaseView = ""
				m.session.CancelRange()
				m.editingAnnotationID = ""
				m.pendingTarget = annotations.Target{}
				m.editor.Blur()
				m.editor.Reset()
				return m, nil
			}
		}
		var cmd tea.Cmd
		m.editor, cmd = m.editor.Update(msg)
		return m, cmd
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.editor.SetWidth(max(20, msg.Width-32))
		m.ensureCursorVisible()
	case tea.KeyMsg:
		if m.prPicker.Active() {
			return m.updatePRPicker(msg)
		}
		if m.jumpPrompt {
			return m.updateJumpPrompt(msg)
		}

		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "?":
			m.pendingKey = ""
			m.showHelp = !m.showHelp
			m.ensureCursorVisible()
		case ":":
			m.pendingKey = ""
			m.jumpPrompt = true
			m.jumpInput = ""
		case "#":
			m.pendingKey = ""
			return m, m.openPRPicker()
		case "g":
			if m.pendingKey == "g" {
				m.pendingKey = ""
				m.jumpTop()
			} else {
				m.pendingKey = "g"
			}
		case "G":
			m.pendingKey = ""
			m.jumpBottom()
		case "[", "]":
			m.pendingKey = msg.String()
		case "h":
			if m.pendingKey == "[" {
				m.pendingKey = ""
				m.prevHunk()
			} else if m.pendingKey == "]" {
				m.pendingKey = ""
				m.nextHunk()
			}
		case "j", "down":
			m.pendingKey = ""
			m.session.MoveLine(1, m.bodyHeight())
		case "k", "up":
			m.pendingKey = ""
			m.session.MoveLine(-1, m.bodyHeight())
		case "n", "right":
			m.pendingKey = ""
			m.session.MoveFile(1)
		case "p", "left":
			m.pendingKey = ""
			m.session.MoveFile(-1)
		case "s":
			m.pendingKey = ""
			m.split = !m.split
		case "x":
			m.pendingKey = ""
			m.syntax = !m.syntax
			m.status = fmt.Sprintf("syntax: %t", m.syntax)
		case "c":
			m.pendingKey = ""
			m.contextDim = !m.contextDim
			m.status = fmt.Sprintf("context dim: %t", m.contextDim)
		case "u":
			m.pendingKey = ""
			m.status = fmt.Sprintf("hide viewed: %t", m.session.ToggleHideViewed())
		case "m":
			m.pendingKey = ""
			m.status = fmt.Sprintf("annotations only: %t", m.session.ToggleAnnotationsOnly())
		case "b":
			m.pendingKey = ""
			m.hideSidebar = !m.hideSidebar
			m.status = fmt.Sprintf("sidebar: %t", !m.hideSidebar)
		case "y":
			m.pendingKey = ""
			if annotation, ok := m.selectedAnnotation(); ok {
				if err := clipboard.WriteAll(annotationMarkdown(annotation)); err != nil {
					m.status = err.Error()
				} else {
					m.status = "annotation copied"
				}
			} else {
				m.status = "no annotation on selected line"
			}
		case "Y":
			m.pendingKey = ""
			if err := clipboard.WriteAll(m.store.ExportMarkdown()); err != nil {
				m.status = err.Error()
			} else {
				m.status = "annotations copied"
			}
		case "w":
			m.pendingKey = ""
			m.wrapCursorLine = !m.wrapCursorLine
			m.status = fmt.Sprintf("wrap cursor line: %t", m.wrapCursorLine)
		case "W":
			m.pendingKey = ""
			m.cfg.IgnoreWhitespace = !m.cfg.IgnoreWhitespace
			if err := m.reload(context.Background()); err != nil {
				m.status = err.Error()
			} else {
				m.status = fmt.Sprintf("ignore whitespace: %t", m.cfg.IgnoreWhitespace)
			}
		case "R":
			m.pendingKey = ""
			if m.refreshing {
				m.status = "refreshing…"
				break
			}
			m.refreshing = true
			m.status = "refreshing…"
			return m, tea.Batch(m.refreshProjectCmd(), loadingSpinnerTick())
		case "v":
			m.pendingKey = ""
			result, err := m.session.ToggleViewed()
			if err != nil {
				m.status = err.Error()
				break
			}
			if result.Path != "" {
				if !result.Viewed {
					m.status = "unmarked viewed"
				} else if m.session.HideViewed() || result.Advanced {
					m.status = "marked viewed"
				} else {
					m.status = "marked viewed; no next unviewed file"
				}
			}
		case "r":
			m.pendingKey = ""
			if m.session.RangeActive() {
				m.session.CancelRange()
				m.status = "range cancelled"
			} else if m.session.StartRange() {
				m.status = "range started; move then press a"
			} else {
				m.status = "range must start on a diff line"
			}
		case "a":
			if m.pendingKey == "[" {
				m.pendingKey = ""
				m.prevAnnotation()
				break
			}
			if m.pendingKey == "]" {
				m.pendingKey = ""
				m.nextAnnotation()
				break
			}
			m.pendingKey = ""
			if m.session.RangeActive() {
				target, err := m.rangeTarget()
				if err != nil {
					m.status = err.Error()
					break
				}
				m.startNewAnnotation(target)
				return m, textarea.Blink
			}
			if annotation, ok := m.selectedAnnotation(); ok {
				if annotation.Source == annotate.SourceGitHub {
					m.status = "github annotation readonly"
					break
				}
				m.startEditAnnotation(annotation)
				return m, textarea.Blink
			}
			if target, err := m.singleLineTarget(); err == nil {
				m.startNewAnnotation(target)
				return m, textarea.Blink
			}
		case "e":
			m.pendingKey = ""
			if annotation, ok := m.selectedAnnotation(); ok {
				if annotation.Source == annotate.SourceGitHub {
					m.status = "github annotation readonly"
					break
				}
				m.startEditAnnotation(annotation)
				return m, textarea.Blink
			}
			m.status = "no annotation on selected line"
		case "d":
			m.pendingKey = ""
			if annotation, ok := m.selectedAnnotation(); ok {
				if annotation.Source == annotate.SourceGitHub {
					m.status = "github annotation readonly"
					break
				}
				if err := m.annotations.Delete(annotation.ID); err != nil {
					m.status = err.Error()
				} else {
					m.status = "annotation deleted"
					if m.session.AnnotationsOnly() {
						m.session.RefreshFilters()
					}
				}
				break
			}
			m.status = "no annotation on selected line"
		}
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
		return view + "\n" + m.editor.View() + "\n⌥+enter save · esc cancel"
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

func (m Model) ready() bool {
	return m.width > 0 && m.height > 0
}

func (m Model) loadingView() string {
	frame := loadingSpinnerFrames[m.loadingFrame%len(loadingSpinnerFrames)]
	return annotationStyle.Render(frame) + " " + dimStyle.Render("opening tdiff…")
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
	m.compareTarget = compareTarget
	m.session.SetSnapshot(s.Files, s.Hash)
	m.syntaxCache = make(map[string]string)
	return nil
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

func (m Model) refreshProjectCmd() tea.Cmd {
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
			return refreshLoadedMsg{err: err}
		}
		snap, err := snapshot.Load(ctx, repo, git.DiffOptions{Mode: cfg.Mode, Base: cfg.Base, IgnoreWhitespace: cfg.IgnoreWhitespace})
		if err != nil {
			return refreshLoadedMsg{err: err}
		}
		msg := refreshLoadedMsg{snap: snap, compareTarget: compareTarget}
		if cfg.Offline {
			msg.offline = true
			return msg
		}
		client := gh.NewClient(repo.Root)
		pr := existingPR
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

func (m Model) selectedAnnotation() (annotate.Annotation, bool) {
	return m.session.SelectedAnnotation()
}

func (m *Model) startEditAnnotation(annotation annotate.Annotation) {
	m.composerBaseView = m.reviewView()
	m.editingAnnotationID = annotation.ID
	m.pendingTarget = annotations.Target{}
	m.editor.Reset()
	m.editor.SetValue(annotation.Body)
	m.composing = true
	m.editor.Focus()
}

func (m *Model) startNewAnnotation(target annotations.Target) {
	m.composerBaseView = m.reviewView()
	m.editingAnnotationID = ""
	m.pendingTarget = target
	m.editor.Reset()
	m.composing = true
	m.editor.Focus()
}

func (m Model) singleLineTarget() (annotations.Target, error) {
	return m.annotations.TargetForDisplayLine(m.selectedLine())
}

func (m Model) rangeTarget() (annotations.Target, error) {
	return m.annotations.TargetForDisplayRange(m.session.RangeLines())
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

func fileStats(f diff.File) diffStats {
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

func (m Model) totalStats() diffStats {
	var total diffStats
	for _, f := range m.session.Files() {
		s := fileStats(f)
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

func (m Model) annotationCount(path string) int {
	if m.store == nil {
		return 0
	}
	return len(m.store.AnnotationsFor(path))
}

func (m Model) totalAnnotationCount() int {
	total := 0
	for _, f := range m.session.Files() {
		total += m.annotationCount(f.Path())
	}
	return total
}

func (m Model) annotationsView(count int) string {
	if count == 0 {
		return ""
	}
	return annotationStyle.Render(fmt.Sprintf("●%d", count))
}

func sidebarHeader(stats, annotations string) string {
	parts := []string{titleStyle.Render("tdiff")}
	if stats != "" {
		parts = append(parts, stats)
	}
	if annotations != "" {
		parts = append(parts, annotations)
	}
	return strings.Join(parts, " ")
}

func selectedSidebarLine(prefix, viewed string, nameW int, path, added, deleted string, annotationCount, annotationW int) string {
	rail := selectedStyle.Render(prefix)
	if strings.Contains(prefix, "▌") {
		rail = selectedAnnotationStyle.Render("▌") + selectedStyle.Render(" ")
	}
	line := rail +
		selectedStyle.Render(viewed+" ") +
		sidebarPath(path, nameW, true, viewed == "✓") +
		selectedStyle.Render(" ") +
		selectedAddStyle.Render(added) +
		selectedStyle.Render(" ") +
		selectedDeleteStyle.Render(deleted) +
		selectedStyle.Render(" ") +
		sidebarAnnotationView(annotationCount, annotationW, true)
	return padRightStyled(line, sidebarWidth, selectedStyle)
}

func sidebarAnnotationView(count, width int, selected bool) string {
	annotation := strings.Repeat(" ", width)
	if count > 0 {
		annotation = fmt.Sprintf("%*s", width, fmt.Sprintf("●%d", count))
	}
	if count == 0 {
		if selected {
			return selectedStyle.Render(annotation)
		}
		return dimStyle.Render(annotation)
	}
	if selected {
		return selectedAnnotationStyle.Render(annotation)
	}
	return annotationStyle.Render(annotation)
}

func sidebarColumnWidths(files []diff.File, annotationCount func(string) int) (int, int, int, int) {
	addW, delW, annotationW := 0, 0, 2
	for _, f := range files {
		stats := fileStats(f)
		addW = max(addW, xansi.StringWidth(sidebarStat("+", stats.Added)))
		delW = max(delW, xansi.StringWidth(sidebarStat("-", stats.Deleted)))
		if annotations := annotationCount(f.Path()); annotations > 0 {
			annotationW = max(annotationW, xansi.StringWidth(fmt.Sprintf("●%d", annotations)))
		}
	}
	nameW := max(8, sidebarWidth-7-addW-delW-annotationW)
	return nameW, addW, delW, annotationW
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
}

func (m *Model) jumpBottom() {
	m.session.JumpBottom(m.bodyHeight())
}

func (m *Model) nextHunk() {
	if !m.session.NextHunk(m.bodyHeight()) {
		m.status = "no next hunk"
	}
}

func (m *Model) prevHunk() {
	if !m.session.PrevHunk(m.bodyHeight()) {
		m.status = "no previous hunk"
	}
}

func (m *Model) nextAnnotation() {
	m.jumpAnnotation(1)
}

func (m *Model) prevAnnotation() {
	m.jumpAnnotation(-1)
}

func (m *Model) jumpAnnotation(delta int) {
	idx, total, ok := m.session.JumpAnnotation(delta, m.bodyHeight())
	if !ok {
		m.status = "no annotations"
		return
	}
	m.status = fmt.Sprintf("annotation %d/%d", idx, total)
}

func (m *Model) jumpToFileLine(line int) bool {
	return m.session.JumpToFileLine(line, m.bodyHeight())
}

func (m *Model) ensureCursorVisible() {
	m.session.EnsureVisible(m.bodyHeight())
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

func (m Model) renderSidebar(height int) string {
	style := lipgloss.NewStyle().Width(sidebarWidth)
	files := m.session.Files()
	fileIdx := m.session.FileIndex()
	nameW, addW, delW, annotationW := sidebarColumnWidths(files, m.annotationCount)
	previewHeight := sidebarAnnotationHeight(height, m.totalAnnotationCount())
	fileHeight := height - previewHeight
	var rows []string
	rows = append(rows, sidebarHeader(m.sidebarStatsView(m.totalStats()), m.annotationsView(m.totalAnnotationCount())))
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
		stats := fileStats(f)
		annotationCount := m.annotationCount(path)
		addedText := fmt.Sprintf("%*s", addW, sidebarStat("+", stats.Added))
		deletedText := fmt.Sprintf("%*s", delW, sidebarStat("-", stats.Deleted))
		added := addStyle.Render(addedText)
		deleted := deleteStyle.Render(deletedText)
		line := fmt.Sprintf("%s%s %s %s %s %s", prefix, viewed, sidebarPath(path, nameW, false, viewed == "✓"), added, deleted, sidebarAnnotationView(annotationCount, annotationW, false))
		if i == fileIdx {
			line = selectedSidebarLine(prefix, viewed, nameW, path, addedText, deletedText, annotationCount, annotationW)
		} else if viewed == "✓" {
			line = dimStyle.Render(line)
		}
		rows = append(rows, line)
	}
	if previewHeight > 0 {
		rows = append(rows, m.renderAnnotationPreview(previewHeight)...)
	}
	return style.Render(strings.Join(rows, "\n"))
}

func sidebarAnnotationHeight(height, annotationCount int) int {
	if annotationCount == 0 || height < sidebarMinFileRows+sidebarMinAnnotationRows {
		return 0
	}
	available := height - sidebarMinFileRows
	if available < sidebarMinAnnotationRows {
		return 0
	}
	desired := sidebarAnnotationHeaderRows + annotationCount*sidebarRowsPerAnnotation
	desired = max(sidebarMinAnnotationRows, desired)
	maxPreview := min(sidebarAnnotationMaxRows, height/2)
	return min(maxPreview, min(available, desired))
}

func (m Model) annotationPositions() []review.AnnotationPosition {
	return m.session.AnnotationPositions()
}
func (m Model) renderAnnotationPreview(maxRows int) []string {
	positions := m.annotationPositions()
	if len(positions) == 0 || maxRows <= 1 {
		return nil
	}
	selected, hasSelected := m.selectedAnnotation()
	rows := []string{dimStyle.Render(""), titleStyle.Render("annotations")}
	remaining := maxRows - len(rows)
	maxItems := max(1, remaining/2)
	start := 0
	if hasSelected {
		for i, p := range positions {
			if p.Annotation.ID == selected.ID {
				start = clamp(i-maxItems/2, 0, max(0, len(positions)-maxItems))
				break
			}
		}
	}
	for i := start; i < len(positions) && remaining > 0; i++ {
		p := positions[i]
		loc := fmt.Sprintf("● %s:%d", compactPath(p.Annotation.Path, sidebarWidth-8), p.Annotation.LineStart)
		if p.Annotation.LineEnd != 0 && p.Annotation.LineEnd != p.Annotation.LineStart {
			loc = fmt.Sprintf("● %s:%d-%d", compactPath(p.Annotation.Path, sidebarWidth-10), p.Annotation.LineStart, p.Annotation.LineEnd)
		}
		bodyText := strings.ReplaceAll(p.Annotation.Body, "\n", " ")
		if author := annotationAuthor(p.Annotation); author != "" {
			bodyText = author + ": " + bodyText
		}
		body := "  " + truncate(bodyText, sidebarWidth-2)
		isSelected := hasSelected && selected.ID == p.Annotation.ID
		if isSelected {
			rows = append(rows, padRightStyled(selectedAnnotationStyle.Render(truncate(loc, sidebarWidth)), sidebarWidth, selectedStyle))
		} else {
			rows = append(rows, annotationStyle.Render(truncate(loc, sidebarWidth)))
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
	stats := m.statsView(fileStats(m.session.Files()[m.session.FileIndex()]))
	if annotations := m.annotationCount(m.currentPath()); annotations > 0 {
		stats = strings.TrimSpace(stats + " " + annotationStyle.Render(fmt.Sprintf("●%d", annotations)))
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
	if annotations := m.totalAnnotationCount(); annotations > 0 {
		stats = strings.TrimSpace(stats + " " + annotationStyle.Render(fmt.Sprintf("●%d", annotations)))
	}
	parts := []string{dimStyle.Render(compareTarget), dimStyle.Render(fmt.Sprintf("%d/%d", min(m.session.FileIndex()+1, len(files)), len(files)))}
	if stats != "" {
		parts = append(parts, stats)
	}
	if m.cfg.Offline {
		parts = append(parts, dimStyle.Render("offline"))
	}
	if m.store != nil && m.store.GitHub != nil && m.store.GitHub.Number > 0 {
		parts = append(parts, dimStyle.Render(fmt.Sprintf("PR #%d", m.store.GitHub.Number)))
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
	if m.session.AnnotationsOnly() {
		parts = append(parts, dimStyle.Render("annotations-only"))
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
		parts = append(parts, annotationStyle.Render(fmt.Sprintf("range %d–%d", start+1, end+1)))
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

func statusView(s string) string {
	lower := strings.ToLower(s)
	if strings.Contains(lower, "invalid") || strings.Contains(lower, "error") || strings.Contains(lower, "failed") || strings.Contains(lower, "unsupported") || strings.Contains(lower, "empty") || strings.HasPrefix(lower, "no ") || strings.Contains(lower, " must ") {
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
		return "j/k extend · a annotate · r cancel"
	}
	if m.showHelp {
		return "? close"
	}
	if _, ok := m.selectedAnnotation(); ok {
		return "e edit · d delete · ]a/[a annotations · y copy"
	}
	return "a annotate · r range · v viewed · # PR · ? help"
}

func (m Model) renderHelp() string {
	lines := []string{
		"nav",
		"  j/k        line up/down",
		"  gg/G       top/bottom",
		"  ]h/[h      next/previous hunk",
		"  ]a/[a      next/previous annotation",
		"  :line      jump to file line",
		"  n/p        next/previous file",
		"  #          attach/change PR",
		"",
		"review",
		"  v          toggle viewed",
		"  u          hide/show viewed files",
		"  m          annotations-only filter",
		"  r          start/cancel range",
		"  a/e/d      annotate/edit/delete",
		"  y/Y        copy selected/all annotations",
		"  ⌥+enter    save annotation",
		"  esc        cancel annotation",
		"",
		"view",
		"  s          split/unified",
		"  b          show/hide sidebar",
		"  x          syntax highlighting",
		"  c          context dimming",
		"  w          wrap cursor line",
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

func (m *Model) saveAnnotation() error {
	target := m.pendingTarget
	if m.editingAnnotationID == "" && target.LineStart == 0 {
		var err error
		target, err = m.singleLineTarget()
		if err != nil {
			return err
		}
	}
	return m.annotations.Save(m.currentPath(), m.session.DiffHash(), m.editingAnnotationID, target, m.editor.Value())
}

const (
	sidebarWidth                = 38
	sidebarMinFileRows          = 8
	sidebarMinAnnotationRows    = 6
	sidebarAnnotationMaxRows    = 24
	sidebarAnnotationHeaderRows = 2
	sidebarRowsPerAnnotation    = 2
	lineNoWidth                 = 4
	intralineContextLines       = 200
	syntaxMaxFileLines          = 2500
	syntaxMaxLineWidth          = 500
	syntaxCacheMaxEntries       = 4000
	statusToastDuration         = 3 * time.Second
)

var (
	loadingSpinnerFrames    = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	brandColor              = lipgloss.Color("180")
	selectedBg              = lipgloss.Color("236")
	addChangedBg            = lipgloss.Color("22")
	deleteChangedBg         = lipgloss.Color("52")
	annotationStyle         = lipgloss.NewStyle().Foreground(brandColor)
	selectedAnnotationStyle = lipgloss.NewStyle().Foreground(brandColor).Background(selectedBg)
	titleStyle              = lipgloss.NewStyle().Bold(true).Foreground(brandColor)
	selectedStyle           = lipgloss.NewStyle().Background(selectedBg)
	rangeBg                 = lipgloss.Color("235")
	rangeStyle              = lipgloss.NewStyle().Background(rangeBg)
	rangeAnnotationStyle    = lipgloss.NewStyle().Foreground(brandColor).Background(rangeBg)
	rangeFooterStyle        = lipgloss.NewStyle().Foreground(brandColor)
	dimStyle                = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	rangeDimStyle           = lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Background(rangeBg)
	selectedDimStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Background(selectedBg)
	successStyle            = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	errorStyle              = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
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

func annotationAuthor(n annotate.Annotation) string {
	if n.GitHub == nil {
		return ""
	}
	if n.GitHub.AuthorName != "" {
		return n.GitHub.AuthorName
	}
	return n.GitHub.AuthorLogin
}

func annotationMarkdown(n annotate.Annotation) string {
	loc := fmt.Sprintf("%s:%d", n.Path, n.LineStart)
	if n.LineEnd != 0 && n.LineEnd != n.LineStart {
		loc = fmt.Sprintf("%s:%d-%d", n.Path, n.LineStart, n.LineEnd)
	}
	body := n.Body
	if author := annotationAuthor(n); author != "" {
		body = author + ": " + body
	}
	out := fmt.Sprintf("- [ ] `%s` (%s) %s", loc, n.Side, body)
	for _, r := range n.Replies {
		author := r.AuthorLogin
		if r.AuthorName != "" {
			author = r.AuthorName
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
