package app

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/owenps/tdiff/internal/annotations"
	"github.com/owenps/tdiff/internal/diff"
	"github.com/owenps/tdiff/internal/git"
	"github.com/owenps/tdiff/internal/notes"
	"github.com/owenps/tdiff/internal/review"
	"github.com/owenps/tdiff/internal/snapshot"
)

type Config struct {
	Base             string
	Mode             git.Mode
	IgnoreWhitespace bool
}

type Model struct {
	repo        git.Repo
	cfg         Config
	store       *notes.Store
	annotations annotations.Workflow

	allFiles []diff.File
	cursor   review.Cursor
	diffHash string

	width  int
	height int

	pendingKey    string
	jumpPrompt    bool
	jumpInput     string
	split         bool
	syntax        bool
	showHelp      bool
	hideViewed    bool
	notesOnly     bool
	hideSidebar   bool
	composing     bool
	editingNoteID string
	pendingTarget annotations.Target
	editor        textarea.Model
	status        string
	statusID      int
	syntaxCache   map[string]string
}

func New(ctx context.Context, cfg Config) (Model, error) {
	repo, err := git.Open(ctx)
	if err != nil {
		return Model{}, err
	}
	store, err := notes.Open(repo.Root)
	if err != nil {
		return Model{}, err
	}
	m := Model{repo: repo, cfg: cfg, store: store, annotations: annotations.NewWorkflow(store), syntax: true, syntaxCache: make(map[string]string)}
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

func (m Model) Init() tea.Cmd { return textarea.Blink }

type clearStatusMsg struct{ id int }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
					m.cursor.CancelRange()
					m.editingNoteID = ""
					m.pendingTarget = annotations.Target{}
					m.editor.Blur()
					m.editor.Reset()
				}
				return m, m.statusToastCmd(previousStatus)
			case "esc":
				m.composing = false
				m.cursor.CancelRange()
				m.editingNoteID = ""
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
			m.cursor.MoveLine(1, m.bodyHeight())
		case "k", "up":
			m.pendingKey = ""
			m.cursor.MoveLine(-1, m.bodyHeight())
		case "n", "right":
			m.pendingKey = ""
			m.cursor.MoveFile(1)
		case "p", "left":
			m.pendingKey = ""
			m.cursor.MoveFile(-1)
		case "s":
			m.pendingKey = ""
			m.split = !m.split
		case "x":
			m.pendingKey = ""
			m.syntax = !m.syntax
			m.status = fmt.Sprintf("syntax: %t", m.syntax)
		case "u":
			m.pendingKey = ""
			m.hideViewed = !m.hideViewed
			m.applyFilters()
			m.status = fmt.Sprintf("hide viewed: %t", m.hideViewed)
		case "m":
			m.pendingKey = ""
			m.notesOnly = !m.notesOnly
			m.applyFilters()
			m.status = fmt.Sprintf("notes only: %t", m.notesOnly)
		case "b":
			m.pendingKey = ""
			m.hideSidebar = !m.hideSidebar
			m.status = fmt.Sprintf("sidebar: %t", !m.hideSidebar)
		case "y":
			m.pendingKey = ""
			if note, ok := m.selectedAnnotation(); ok {
				if err := clipboard.WriteAll(noteMarkdown(note)); err != nil {
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
			m.cfg.IgnoreWhitespace = !m.cfg.IgnoreWhitespace
			if err := m.reload(context.Background()); err != nil {
				m.status = err.Error()
			} else {
				m.status = fmt.Sprintf("ignore whitespace: %t", m.cfg.IgnoreWhitespace)
			}
		case "v":
			m.pendingKey = ""
			path := m.currentPath()
			if path != "" {
				if m.store.IsViewed(path, m.diffHash) {
					_ = m.store.ClearViewed(path)
					m.status = "unmarked viewed"
				} else {
					_ = m.store.MarkViewed(path, m.diffHash)
					if m.hideViewed {
						m.applyFilters()
						m.status = "marked viewed"
					} else if m.advanceToNextUnviewed() {
						m.status = "marked viewed"
					} else {
						m.status = "marked viewed; no next unviewed file"
					}
				}
			}
		case "r":
			m.pendingKey = ""
			if m.cursor.RangeActive() {
				m.cursor.CancelRange()
				m.status = "range cancelled"
			} else if m.cursor.StartRange() {
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
			if m.cursor.RangeActive() {
				target, err := m.rangeTarget()
				if err != nil {
					m.status = err.Error()
					break
				}
				m.startNewAnnotation(target)
				return m, textarea.Blink
			}
			if note, ok := m.selectedAnnotation(); ok {
				m.startEditAnnotation(note)
				return m, textarea.Blink
			}
			if target, err := m.singleLineTarget(); err == nil {
				m.startNewAnnotation(target)
				return m, textarea.Blink
			}
		case "e":
			m.pendingKey = ""
			if note, ok := m.selectedAnnotation(); ok {
				m.startEditAnnotation(note)
				return m, textarea.Blink
			}
			m.status = "no annotation on selected line"
		case "d":
			m.pendingKey = ""
			if note, ok := m.selectedAnnotation(); ok {
				if err := m.annotations.Delete(note.ID); err != nil {
					m.status = err.Error()
				} else {
					m.status = "annotation deleted"
					if m.notesOnly {
						m.applyFilters()
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
	if len(m.cursor.Files()) == 0 {
		if len(m.allFiles) > 0 {
			return dimStyle.Render("no files match filters · press u/m") + "\n"
		}
		return dimStyle.Render("clean tree · nothing to review") + "\n"
	}
	if m.width == 0 {
		m.width = 100
		m.height = 30
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
	status := m.renderStatus()
	view := body + "\n" + status
	if m.composing {
		return view + "\n" + m.editor.View() + "\n⌥+enter save · esc cancel"
	}
	if m.showHelp {
		return overlay(view, m.renderHelp(), m.width, m.height)
	}
	return view
}

func (m *Model) reload(ctx context.Context) error {
	s, err := snapshot.Load(ctx, m.repo, git.DiffOptions{Mode: m.cfg.Mode, Base: m.cfg.Base, IgnoreWhitespace: m.cfg.IgnoreWhitespace})
	if err != nil {
		return err
	}
	m.allFiles = s.Files
	m.diffHash = s.Hash
	m.syntaxCache = make(map[string]string)
	m.applyFilters()
	return nil
}

func (m *Model) applyFilters() {
	filtered := make([]diff.File, 0, len(m.allFiles))
	for _, f := range m.allFiles {
		path := f.Path()
		if m.hideViewed && m.store.IsViewed(path, m.diffHash) {
			continue
		}
		if m.notesOnly && m.noteCount(path) == 0 {
			continue
		}
		filtered = append(filtered, f)
	}
	m.cursor.SetFiles(filtered)
}

type displayLine = review.DisplayLine

func (m Model) currentLines() []displayLine {
	return m.cursor.CurrentLines()
}

func (m Model) selectedLine() displayLine {
	return m.cursor.SelectedLine()
}

func (m Model) selectedAnnotation() (notes.Note, bool) {
	dl := m.selectedLine()
	if dl.Line == nil {
		return notes.Note{}, false
	}
	return m.annotations.AnnotationAt(m.currentPath(), *dl.Line)
}

func (m *Model) startEditAnnotation(note notes.Note) {
	m.editingNoteID = note.ID
	m.pendingTarget = annotations.Target{}
	m.editor.Reset()
	m.editor.SetValue(note.Body)
	m.composing = true
	m.editor.Focus()
}

func (m *Model) startNewAnnotation(target annotations.Target) {
	m.editingNoteID = ""
	m.pendingTarget = target
	m.editor.Reset()
	m.composing = true
	m.editor.Focus()
}

func (m Model) singleLineTarget() (annotations.Target, error) {
	dl := m.selectedLine()
	if dl.Line == nil {
		return annotations.Target{}, fmt.Errorf("no line selected")
	}
	return m.annotations.TargetForLine(annotations.DiffLine{Line: *dl.Line, HunkHeader: dl.HunkHeader})
}

func (m Model) rangeTarget() (annotations.Target, error) {
	var selected []annotations.DiffLine
	for _, dl := range m.cursor.RangeLines() {
		if dl.Line == nil {
			continue
		}
		selected = append(selected, annotations.DiffLine{Line: *dl.Line, HunkHeader: dl.HunkHeader})
	}
	return m.annotations.TargetForRange(selected)
}

func (m Model) currentPath() string {
	return m.cursor.CurrentPath()
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
	for _, f := range m.cursor.Files() {
		s := fileStats(f)
		total.Added += s.Added
		total.Deleted += s.Deleted
	}
	return total
}

func (m Model) statsView(s diffStats) string {
	return fmt.Sprintf("%s %s", addStyle.Render(fmt.Sprintf("+%d", s.Added)), deleteStyle.Render(fmt.Sprintf("-%d", s.Deleted)))
}

func (m Model) noteCount(path string) int {
	return len(m.store.NotesFor(path))
}

func (m Model) totalNoteCount() int {
	total := 0
	for _, f := range m.cursor.Files() {
		total += m.noteCount(f.Path())
	}
	return total
}

func (m Model) notesView(count int) string {
	if count == 0 {
		return dimStyle.Render("  ")
	}
	return annotationStyle.Render(fmt.Sprintf("●%d", count))
}

func selectedSidebarLine(prefix, viewed string, nameW int, path, added, deleted string, noteCount, noteW int) string {
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
		sidebarNoteView(noteCount, noteW, true)
	return padRightStyled(line, sidebarWidth, selectedStyle)
}

func sidebarNoteView(count, width int, selected bool) string {
	note := strings.Repeat(" ", width)
	if count > 0 {
		note = fmt.Sprintf("%*s", width, fmt.Sprintf("●%d", count))
	}
	if count == 0 {
		if selected {
			return selectedStyle.Render(note)
		}
		return dimStyle.Render(note)
	}
	if selected {
		return selectedAnnotationStyle.Render(note)
	}
	return annotationStyle.Render(note)
}

func sidebarColumnWidths(files []diff.File, noteCount func(string) int) (int, int, int, int) {
	addW, delW, noteW := 2, 2, 2
	for _, f := range files {
		stats := fileStats(f)
		addW = max(addW, xansi.StringWidth(fmt.Sprintf("+%d", stats.Added)))
		delW = max(delW, xansi.StringWidth(fmt.Sprintf("-%d", stats.Deleted)))
		if notes := noteCount(f.Path()); notes > 0 {
			noteW = max(noteW, xansi.StringWidth(fmt.Sprintf("●%d", notes)))
		}
	}
	nameW := max(8, sidebarWidth-7-addW-delW-noteW)
	return nameW, addW, delW, noteW
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
	m.cursor.JumpTop()
}

func (m *Model) jumpBottom() {
	m.cursor.JumpBottom(m.bodyHeight())
}

func (m *Model) nextHunk() {
	if !m.cursor.NextHunk(m.bodyHeight()) {
		m.status = "no next hunk"
	}
}

func (m *Model) prevHunk() {
	if !m.cursor.PrevHunk(m.bodyHeight()) {
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
	positions := m.annotationPositions()
	if len(positions) == 0 {
		m.status = "no annotations"
		return
	}
	curFile, curLine := m.cursor.FileIndex(), m.cursor.LineIndex()
	idx := -1
	if delta > 0 {
		idx = 0
		for i, p := range positions {
			if p.fileIdx > curFile || (p.fileIdx == curFile && p.lineIdx > curLine) {
				idx = i
				break
			}
		}
	} else {
		idx = len(positions) - 1
		for i := len(positions) - 1; i >= 0; i-- {
			p := positions[i]
			if p.fileIdx < curFile || (p.fileIdx == curFile && p.lineIdx < curLine) {
				idx = i
				break
			}
		}
	}
	p := positions[idx]
	if m.cursor.JumpToIndex(p.fileIdx, p.lineIdx, m.bodyHeight()) {
		m.status = fmt.Sprintf("annotation %d/%d", idx+1, len(positions))
	}
}

func (m *Model) jumpToFileLine(line int) bool {
	return m.cursor.JumpToFileLine(line, m.bodyHeight())
}

func (m *Model) ensureCursorVisible() {
	m.cursor.EnsureVisible(m.bodyHeight())
}

func (m Model) bodyHeight() int {
	if m.height == 0 {
		return 28
	}
	return max(1, m.height-1-m.footerHeight())
}

func (m Model) footerHeight() int {
	return 1
}

func (m *Model) advanceToNextUnviewed() bool {
	return m.cursor.AdvanceToNextFile(func(f diff.File) bool {
		return !m.store.IsViewed(f.Path(), m.diffHash)
	})
}

func (m Model) renderSidebar(height int) string {
	style := lipgloss.NewStyle().Width(sidebarWidth)
	files := m.cursor.Files()
	fileIdx := m.cursor.FileIndex()
	nameW, addW, delW, noteW := sidebarColumnWidths(files, m.noteCount)
	previewHeight := 0
	if height >= 12 && m.totalNoteCount() > 0 {
		previewHeight = min(7, height/3)
	}
	fileHeight := height - previewHeight
	var rows []string
	rows = append(rows, titleStyle.Render("tdiff")+" "+m.statsView(m.totalStats())+" "+m.notesView(m.totalNoteCount()))
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
		if m.store.IsViewed(path, m.diffHash) {
			viewed = "✓"
		}
		stats := fileStats(f)
		noteCount := m.noteCount(path)
		addedText := fmt.Sprintf("%*s", addW, fmt.Sprintf("+%d", stats.Added))
		deletedText := fmt.Sprintf("%*s", delW, fmt.Sprintf("-%d", stats.Deleted))
		added := addStyle.Render(addedText)
		deleted := deleteStyle.Render(deletedText)
		line := fmt.Sprintf("%s%s %s %s %s %s", prefix, viewed, sidebarPath(path, nameW, false, viewed == "✓"), added, deleted, sidebarNoteView(noteCount, noteW, false))
		if i == fileIdx {
			line = selectedSidebarLine(prefix, viewed, nameW, path, addedText, deletedText, noteCount, noteW)
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

type annotationPosition struct {
	fileIdx int
	lineIdx int
	note    notes.Note
}

func (m Model) annotationPositions() []annotationPosition {
	files := m.cursor.Files()
	var out []annotationPosition
	for fileIdx, f := range files {
		path := f.Path()
		lines := displayLinesForFile(f)
		for _, note := range m.store.NotesFor(path) {
			for lineIdx, dl := range lines {
				if dl.Line != nil && noteMatchesLine(note, *dl.Line) {
					out = append(out, annotationPosition{fileIdx: fileIdx, lineIdx: lineIdx, note: note})
					break
				}
			}
		}
	}
	return out
}

func displayLinesForFile(f diff.File) []displayLine {
	var out []displayLine
	for _, h := range f.Hunks {
		header := h.Header
		out = append(out, displayLine{Text: header, HunkHeader: header})
		for i := range h.Lines {
			line := h.Lines[i]
			out = append(out, displayLine{Line: &line, Text: line.Text, HunkHeader: header})
		}
	}
	return out
}

func noteMatchesLine(note notes.Note, line diff.Line) bool {
	lineNo := line.NewNo
	if note.Side == notes.SideOld {
		lineNo = line.OldNo
	}
	return lineNo >= note.LineStart && lineNo <= note.LineEnd
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
			if p.note.ID == selected.ID {
				start = clamp(i-maxItems/2, 0, max(0, len(positions)-maxItems))
				break
			}
		}
	}
	for i := start; i < len(positions) && remaining > 0; i++ {
		p := positions[i]
		loc := fmt.Sprintf("● %s:%d", compactPath(p.note.Path, sidebarWidth-8), p.note.LineStart)
		if p.note.LineEnd != 0 && p.note.LineEnd != p.note.LineStart {
			loc = fmt.Sprintf("● %s:%d-%d", compactPath(p.note.Path, sidebarWidth-10), p.note.LineStart, p.note.LineEnd)
		}
		body := "  " + truncate(strings.ReplaceAll(p.note.Body, "\n", " "), sidebarWidth-2)
		isSelected := hasSelected && selected.ID == p.note.ID
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
	stats := m.statsView(fileStats(m.cursor.Files()[m.cursor.FileIndex()]))
	if notes := m.noteCount(m.currentPath()); notes > 0 {
		stats += " " + annotationStyle.Render(fmt.Sprintf("●%d", notes))
	}
	line := titleStyle.Render(path) + "  " + stats
	return padRight(truncate(line, width), width)
}

func (m Model) renderDiff(height int) string {
	width := max(40, m.width-sidebarWidth-2)
	if m.hideSidebar {
		width = max(40, m.width)
	}
	style := lipgloss.NewStyle().Width(width)
	lineCount := m.cursor.CurrentLineCount()
	lineIdx := m.cursor.LineIndex()
	start := clamp(m.cursor.DiffOffset(), 0, max(0, lineCount-height))
	end := min(lineCount, start+height)
	lines := m.cursor.CurrentLinesRange(start, end)
	syntaxOK := m.syntaxAllowed(lineCount)

	notesForFile := m.store.NotesFor(m.currentPath())
	var rows []string
	for offset, dl := range lines {
		i := start + offset
		selected := i == lineIdx
		inRange := m.inActiveRange(i)
		rangeGlyph := m.rangeGlyph(i)
		line := m.formatLine(dl, notesForFile, width, selected, inRange, rangeGlyph, syntaxOK)
		if !selected {
			if inRange {
				line = padRightStyled(line, width, rangeStyle)
			} else {
				line = padRight(line, width)
			}
		}
		rows = append(rows, line)
	}
	return style.Render(strings.Join(rows, "\n"))
}

func (m Model) syntaxAllowed(lineCount int) bool {
	return m.syntax && lineCount <= syntaxMaxFileLines && lexers.Match(m.currentPath()) != nil
}

func (m Model) formatLine(dl displayLine, fileNotes []notes.Note, width int, selected, inRange bool, rangeGlyph string, syntaxOK bool) string {
	if dl.Line == nil {
		text := truncate(rangePrefix(rangeGlyph)+dl.Text, width)
		if selected {
			return padRightStyled(selectedHunkStyle.Render(text), width, selectedStyle)
		}
		if inRange {
			return rangeHunkStyle.Render(text)
		}
		return hunkStyle.Render(text)
	}
	l := *dl.Line
	marker := " "
	for _, n := range fileNotes {
		if noteMarker := m.annotations.MarkerFor(n, l); noteMarker != "" {
			marker = noteMarker
			break
		}
	}
	if m.split {
		oldText, newText := "", ""
		oldNo, newNo := "", ""
		if l.OldNo > 0 {
			oldNo = fmt.Sprintf("%2d", l.OldNo)
		}
		if l.NewNo > 0 {
			newNo = fmt.Sprintf("%2d", l.NewNo)
		}
		body := strings.TrimPrefix(strings.TrimPrefix(l.Text, "+"), "-")
		switch l.Kind {
		case diff.Delete:
			oldText = body
		case diff.Add:
			newText = body
		default:
			oldText = body
			newText = body
		}
		fixed := 10
		leftW := max(1, (width-fixed)/2)
		rightW := max(1, width-fixed-leftW)
		prefix := rangeCell(rangeGlyph, selected, inRange) + gutterView(" ", selected, inRange) + annotationMarker(marker, selected, inRange)
		oldBody := fmt.Sprintf("%-*s", leftW, truncate(oldText, leftW))
		newBody := fmt.Sprintf("%-*s", rightW, truncate(newText, rightW))
		oldKind, newKind := diff.Context, diff.Context
		if l.Kind == diff.Delete {
			oldKind = diff.Delete
		}
		if l.Kind == diff.Add {
			newKind = diff.Add
		}
		rest := m.lineNoView(oldNo, oldKind, selected, inRange) + gutterView(" │ ", selected, inRange) + m.splitTextView(l.Kind, notes.SideOld, oldBody, selected, inRange, syntaxOK) + gutterView(" │ ", selected, inRange) + m.lineNoView(newNo, newKind, selected, inRange) + gutterView(" ", selected, inRange) + m.splitTextView(l.Kind, notes.SideNew, newBody, selected, inRange, syntaxOK)
		if selected {
			line := rangeCell(rangeGlyph, true, inRange) + selectedStyle.Render(" ") + annotationMarker(marker, true, inRange) + rest
			return padRightStyled(truncate(line, width), width, selectedStyle)
		}
		return truncate(prefix+rest, width)
	}
	oldNo, newNo := "", ""
	if l.OldNo > 0 {
		oldNo = fmt.Sprintf("%2d", l.OldNo)
	}
	if l.NewNo > 0 {
		newNo = fmt.Sprintf("%2d", l.NewNo)
	}
	prefix := rangeCell(rangeGlyph, selected, inRange) + gutterView(" ", selected, inRange) + annotationMarker(marker, selected, inRange)
	rest := m.lineNoView(oldNo, l.Kind, selected, inRange) + gutterView(" ", selected, inRange) + m.lineNoView(newNo, l.Kind, selected, inRange) + gutterView(" ", selected, inRange) + m.diffTextView(l.Kind, l.Text, selected, inRange, syntaxOK)
	if selected {
		line := rangeCell(rangeGlyph, true, inRange) + selectedStyle.Render(" ") + annotationMarker(marker, true, inRange) + rest
		return padRightStyled(truncate(line, width), width, selectedStyle)
	}
	return truncate(prefix+rest, width)
}

func (m Model) lineNoView(s string, kind diff.Kind, selected, inRange bool) string {
	if selected {
		return selectedColorLine(kind, s)
	}
	if inRange {
		return rangeColorLine(kind, s)
	}
	return colorLine(kind, s)
}

func gutterView(s string, selected, inRange bool) string {
	if selected {
		return selectedDimStyle.Render(s)
	}
	if inRange {
		return rangeDimStyle.Render(s)
	}
	return dimStyle.Render(s)
}

func (m Model) diffTextView(kind diff.Kind, s string, selected, inRange, syntaxOK bool) string {
	if !syntaxOK || kind == diff.Meta {
		return diffTextColor(kind, s, selected, inRange)
	}
	marker, body := diffMarkerBody(s)
	if selected {
		prefix := ""
		if marker != "" {
			prefix = selectedColorLine(kind, marker)
		}
		return prefix + withANSIBackground(m.syntaxView(body), selectedBg)
	}
	if inRange {
		prefix := ""
		if marker != "" {
			prefix = rangeColorLine(kind, marker)
		}
		return prefix + withANSIBackground(m.syntaxView(body), rangeBg)
	}
	if marker != "" {
		return colorLine(kind, marker) + m.syntaxView(body)
	}
	return m.syntaxView(body)
}

func (m Model) splitTextView(kind diff.Kind, side notes.Side, s string, selected, inRange, syntaxOK bool) string {
	if kind == diff.Delete && side == notes.SideOld {
		return m.syntaxBodyView(kind, s, selected, inRange, syntaxOK)
	}
	if kind == diff.Add && side == notes.SideNew {
		return m.syntaxBodyView(kind, s, selected, inRange, syntaxOK)
	}
	if selected {
		if syntaxOK {
			return withANSIBackground(m.syntaxView(s), selectedBg)
		}
		return selectedStyle.Render(s)
	}
	if inRange {
		if syntaxOK {
			return withANSIBackground(m.syntaxView(s), rangeBg)
		}
		return rangeStyle.Render(s)
	}
	if syntaxOK {
		return m.syntaxView(s)
	}
	return s
}

func (m Model) syntaxBodyView(kind diff.Kind, s string, selected, inRange, syntaxOK bool) string {
	if !syntaxOK {
		return diffTextColor(kind, s, selected, inRange)
	}
	if selected {
		return withANSIBackground(m.syntaxView(s), selectedBg)
	}
	if inRange {
		return withANSIBackground(m.syntaxView(s), rangeBg)
	}
	return m.syntaxView(s)
}

func withANSIBackground(s string, color lipgloss.Color) string {
	if s == "" {
		return s
	}
	bg := "\x1b[48;5;" + string(color) + "m"
	return bg + strings.ReplaceAll(s, "\x1b[0m", "\x1b[0m"+bg) + "\x1b[0m"
}

func diffTextColor(kind diff.Kind, s string, selected, inRange bool) string {
	if selected {
		return selectedColorLine(kind, s)
	}
	if inRange {
		return rangeColorLine(kind, s)
	}
	return colorLine(kind, s)
}

func diffMarkerBody(s string) (string, string) {
	if s == "" {
		return "", ""
	}
	switch s[0] {
	case '+', '-', ' ':
		return s[:1], s[1:]
	default:
		return "", s
	}
}

func (m Model) syntaxView(s string) string {
	if strings.TrimSpace(s) == "" || xansi.StringWidth(s) > syntaxMaxLineWidth {
		return s
	}
	path := m.currentPath()
	lexer := lexers.Match(path)
	if lexer == nil {
		return s
	}
	body := strings.TrimRight(s, " \t")
	trail := s[len(body):]
	key := path + "\x00" + body
	if m.syntaxCache != nil {
		if cached, ok := m.syntaxCache[key]; ok {
			return cached + trail
		}
		if len(m.syntaxCache) >= syntaxCacheMaxEntries {
			clear(m.syntaxCache)
		}
	}
	formatter := formatters.Get("terminal256")
	style := styles.Get("github-dark")
	if formatter == nil || style == nil {
		return s
	}
	iterator, err := lexer.Tokenise(nil, body)
	if err != nil {
		return s
	}
	var out bytes.Buffer
	if err := formatter.Format(&out, style, iterator); err != nil {
		return s
	}
	highlighted := strings.TrimSuffix(out.String(), "\n")
	if m.syntaxCache != nil {
		m.syntaxCache[key] = highlighted
	}
	return highlighted + trail
}

func (m Model) rangeGlyph(idx int) string {
	if !m.cursor.RangeActive() || !m.cursor.InActiveRange(idx) {
		return " "
	}
	start, end := m.cursor.RangeIndexes()
	if start == end {
		return "│"
	}
	if idx == start {
		return "╭"
	}
	if idx == end {
		return "╰"
	}
	return "│"
}

func rangeCell(glyph string, selected, inRange bool) string {
	if glyph == "" {
		glyph = " "
	}
	if strings.TrimSpace(glyph) == "" {
		if selected {
			return selectedStyle.Render(" ")
		}
		if inRange {
			return rangeStyle.Render(" ")
		}
		return " "
	}
	if selected {
		return selectedAnnotationStyle.Render(glyph)
	}
	if inRange {
		return rangeAnnotationStyle.Render(glyph)
	}
	return annotationStyle.Render(glyph)
}

func rangePrefix(glyph string) string {
	if strings.TrimSpace(glyph) == "" {
		return ""
	}
	return glyph + " "
}

func annotationMarker(marker string, selected, inRange bool) string {
	if strings.TrimSpace(marker) == "" {
		if selected {
			return selectedStyle.Render(marker)
		}
		if inRange {
			return rangeStyle.Render(marker)
		}
		return marker
	}
	if selected {
		return selectedAnnotationStyle.Render(marker)
	}
	if inRange {
		return rangeAnnotationStyle.Render(marker)
	}
	return annotationStyle.Render(marker)
}

func (m Model) inActiveRange(idx int) bool {
	return m.cursor.InActiveRange(idx)
}
func (m Model) renderStatus() string {
	mode := string(m.cfg.Mode)
	if mode == "" {
		mode = string(git.ModeBranch)
	}
	files := m.cursor.Files()
	stats := m.statsView(m.totalStats())
	if notes := m.totalNoteCount(); notes > 0 {
		stats += " " + annotationStyle.Render(fmt.Sprintf("●%d", notes))
	}
	parts := []string{dimStyle.Render(mode), dimStyle.Render(fmt.Sprintf("%d/%d", min(m.cursor.FileIndex()+1, len(files)), len(files))), stats}
	if m.split {
		parts = append(parts, dimStyle.Render("split"))
	}
	if m.cfg.IgnoreWhitespace {
		parts = append(parts, dimStyle.Render("ignore-space"))
	}
	if m.hideViewed {
		parts = append(parts, dimStyle.Render("hide-viewed"))
	}
	if m.notesOnly {
		parts = append(parts, dimStyle.Render("notes-only"))
	}
	if m.hideSidebar {
		parts = append(parts, dimStyle.Render("sidebar-off"))
	}
	if !m.syntax {
		parts = append(parts, dimStyle.Render("syntax-off"))
	} else if !m.syntaxAllowed(m.cursor.CurrentLineCount()) {
		parts = append(parts, dimStyle.Render("syntax-skipped"))
	}
	if m.cursor.RangeActive() {
		start, end := m.cursor.RangeIndexes()
		parts = append(parts, annotationStyle.Render(fmt.Sprintf("range %d–%d", start+1, end+1)))
	}
	if m.status != "" {
		parts = append(parts, statusView(m.status))
	}
	left := joinDim(parts, " · ")
	right := m.footerHints()
	sep := " · "
	gap := max(1, m.width-xansi.StringWidth(left)-xansi.StringWidth(right)-xansi.StringWidth(sep)-1)
	tailStyle := dimStyle
	if m.cursor.RangeActive() {
		tailStyle = rangeFooterStyle
	}
	line := left + strings.Repeat(" ", gap) + tailStyle.Render(sep+right)
	out := truncate(line, m.width-1)
	if m.cursor.RangeActive() {
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
	if m.jumpPrompt {
		return ":" + m.jumpInput + "  enter jump · esc cancel"
	}
	if m.cursor.RangeActive() {
		return "j/k extend · a annotate · r cancel"
	}
	if m.showHelp {
		return "? close"
	}
	if _, ok := m.selectedAnnotation(); ok {
		return "e edit · d delete · ]a/[a notes · y copy"
	}
	return "a note · r range · v viewed · b sidebar · ? keys"
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
		"",
		"review",
		"  v          toggle viewed",
		"  u          hide/show viewed files",
		"  m          notes-only filter",
		"  r          start/cancel range",
		"  a/e/d      add/edit/delete note",
		"  y/Y        copy selected/all notes",
		"  ⌥+enter    save note",
		"  esc        cancel note",
		"",
		"view",
		"  s          split/unified",
		"  b          show/hide sidebar",
		"  x          syntax highlighting",
		"  w          whitespace",
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
	if m.editingNoteID == "" && target.LineStart == 0 {
		var err error
		target, err = m.singleLineTarget()
		if err != nil {
			return err
		}
	}
	return m.annotations.Save(m.currentPath(), m.diffHash, m.editingNoteID, target, m.editor.Value())
}

const (
	sidebarWidth          = 38
	syntaxMaxFileLines    = 2500
	syntaxMaxLineWidth    = 500
	syntaxCacheMaxEntries = 4000
	statusToastDuration   = 3 * time.Second
)

var (
	brandColor              = lipgloss.Color("180")
	selectedBg              = lipgloss.Color("236")
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
	hunkStyle               = lipgloss.NewStyle().Foreground(lipgloss.Color("99"))
	selectedHunkStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("99")).Background(selectedBg)
	rangeHunkStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("99")).Background(rangeBg)
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

func noteMarkdown(n notes.Note) string {
	loc := fmt.Sprintf("%s:%d", n.Path, n.LineStart)
	if n.LineEnd != 0 && n.LineEnd != n.LineStart {
		loc = fmt.Sprintf("%s:%d-%d", n.Path, n.LineStart, n.LineEnd)
	}
	out := fmt.Sprintf("- [ ] `%s` (%s) %s", loc, n.Side, n.Body)
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
