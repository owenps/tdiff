package app

import (
	"bytes"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/owenps/tdiff/internal/diff"
	"github.com/owenps/tdiff/internal/review"
	"github.com/owenps/tdiff/internal/thread"
	"github.com/owenps/tdiff/internal/threadtarget"
)

const inlineThreadMinScreenRows = 8

type threadMarkers map[thread.Side]map[int]string

func newThreadMarkers(threads []thread.Thread) threadMarkers {
	markers := threadMarkers{thread.SideOld: {}, thread.SideNew: {}}
	for _, thread := range threads {
		start, end := threadtarget.Range(thread)
		if start <= 0 {
			continue
		}
		if end < start {
			end = start
		}
		for line := start; line <= end; line++ {
			glyph := "│"
			if start == end || line == start {
				glyph = threadGlyph(thread)
			} else if line == end {
				glyph = "╰"
			}
			if markers[thread.Side][line] == "" {
				markers[thread.Side][line] = glyph
			}
		}
	}
	return markers
}

func threadGlyph(t thread.Thread) string {
	if thread.UnreadForHuman(t) {
		return "●"
	}
	return "○"
}

func (m threadMarkers) markerForLine(line diff.Line) string {
	if side := m[thread.SideOld]; side != nil && line.OldNo > 0 {
		if marker := side[line.OldNo]; marker != "" {
			return marker
		}
	}
	if side := m[thread.SideNew]; side != nil && line.NewNo > 0 {
		return side[line.NewNo]
	}
	return ""
}

type diffPane struct {
	path              string
	width             int
	split             bool
	syntax            bool
	contextDim        bool
	wrapCursorLine    bool
	hideLineNumbers   bool
	syntaxCache       map[string]string
	fullWidthHunks    map[string]bool
	splitLayoutRows   []splitRow
	splitOffset       int
	session           review.Session
	markers           threadMarkers
	threads           []thread.Thread
	inlineThreads     bool
	selectedThread    thread.Thread
	hasSelectedThread bool
}

func (m Model) renderDiff(height int) string {
	return m.diffPane(m.diffWidth()).Render(height)
}

func (m Model) diffWidth() int {
	width := max(40, m.width-sidebarWidth-2)
	if m.hideSidebar {
		width = max(40, m.width)
	}
	return width
}

func (m Model) diffPane(width int) diffPane {
	path := m.currentPath()
	var fileThreads []thread.Thread
	if m.store != nil {
		fileThreads = m.store.ThreadsFor(path)
	}
	var fullWidthHunks map[string]bool
	var splitRows []splitRow
	if m.split {
		fullWidthHunks = m.splitFullWidthHunks()
		splitRows = m.splitNavForCurrentFile().rows
	}
	selectedThread, hasSelectedThread := m.selectedThread()
	return diffPane{
		path:              path,
		width:             width,
		split:             m.split,
		syntax:            m.syntax,
		contextDim:        m.contextDim,
		wrapCursorLine:    m.wrapCursorLine,
		hideLineNumbers:   m.hideLineNumbers,
		syntaxCache:       m.syntaxCache,
		fullWidthHunks:    fullWidthHunks,
		splitLayoutRows:   splitRows,
		splitOffset:       m.splitOffset,
		session:           m.session,
		markers:           newThreadMarkers(fileThreads),
		threads:           fileThreads,
		inlineThreads:     !m.hideInlineThreads,
		selectedThread:    selectedThread,
		hasSelectedThread: hasSelectedThread,
	}
}

func (m Model) syntaxAllowed(lineCount int) bool {
	return m.diffPane(0).syntaxAllowed(lineCount)
}

func (m Model) splitFullWidthHunks() map[string]bool {
	path := m.currentPath()
	if path == "" {
		return nil
	}
	if m.splitHunkCache != nil {
		if cached, ok := m.splitHunkCache[path]; ok {
			return cached
		}
	}
	files := m.session.Files()
	idx := m.session.FileIndex()
	if idx < 0 || idx >= len(files) {
		return nil
	}
	out := fullWidthHunksForFile(files[idx])
	if m.splitHunkCache != nil {
		m.splitHunkCache[path] = out
	}
	return out
}

func fullWidthHunksForFile(file diff.File) map[string]bool {
	out := make(map[string]bool, len(file.Hunks))
	for _, hunk := range file.Hunks {
		hasChange := false
		hasReplacement := false
		for i := 0; i < len(hunk.Lines); i++ {
			line := hunk.Lines[i]
			if line.Kind == diff.Add || line.Kind == diff.Delete {
				hasChange = true
			}
			if line.Kind != diff.Delete {
				continue
			}
			j := i
			for j < len(hunk.Lines) && hunk.Lines[j].Kind == diff.Delete {
				j++
			}
			if j < len(hunk.Lines) && hunk.Lines[j].Kind == diff.Add {
				hasReplacement = true
				break
			}
		}
		out[hunk.Header] = hasChange && !hasReplacement
	}
	return out
}

type splitNav struct {
	rows      []splitRow
	lineToRow map[int]int
}

func (m *Model) moveLine(delta int) {
	if !m.split {
		m.session.MoveLine(delta, m.bodyHeight())
		return
	}
	nav := m.splitNavForCurrentFile()
	if len(nav.rows) == 0 {
		return
	}
	current := m.session.LineIndex()
	rowIdx, ok := nav.lineToRow[current]
	if !ok {
		m.session.MoveLine(delta, m.bodyHeight())
		return
	}
	targetLine := -1
	if m.session.RangeActive() {
		targetLine = splitNavLineForSide(nav.rows, rowIdx, delta, m.session.RangeSide())
	} else {
		targetRow := clamp(rowIdx+delta, 0, len(nav.rows)-1)
		targetLine = splitNavRowTargetLine(nav.rows[targetRow], delta)
	}
	if targetLine < 0 || targetLine == current {
		return
	}
	if m.session.RangeActive() {
		m.session.JumpToLine(targetLine, m.bodyHeight())
	} else {
		m.session.JumpToIndex(m.session.FileIndex(), targetLine, m.bodyHeight())
	}
	m.ensureSplitCursorVisible(m.bodyHeight())
}

func (m Model) splitNavForCurrentFile() splitNav {
	path := m.currentPath()
	if path == "" {
		return splitNav{}
	}
	if m.splitNavCache != nil {
		if cached, ok := m.splitNavCache[path]; ok {
			return cached
		}
	}
	pane := diffPane{fullWidthHunks: m.splitFullWidthHunks(), session: m.session}
	rows := pane.splitRows(m.currentLines(), 0)
	nav := splitNav{rows: rows, lineToRow: make(map[int]int, len(rows)*2)}
	for rowIdx, row := range rows {
		if row.oldIdx >= 0 {
			nav.lineToRow[row.oldIdx] = rowIdx
		}
		if row.newIdx >= 0 {
			nav.lineToRow[row.newIdx] = rowIdx
		}
	}
	if m.splitNavCache != nil {
		m.splitNavCache[path] = nav
	}
	return nav
}

func (m *Model) ensureSplitCursorVisible(height int) {
	if !m.split {
		return
	}
	nav := m.splitNavForCurrentFile()
	rowIdx, ok := nav.lineToRow[m.session.LineIndex()]
	if !ok {
		m.splitOffset = 0
		return
	}
	rowHeight := m.splitRenderedRowHeight(nav.rows[rowIdx])
	if rowIdx < m.splitOffset {
		m.splitOffset = rowIdx
	}
	if rowIdx+rowHeight > m.splitOffset+height {
		m.splitOffset = rowIdx + rowHeight - height
	}
	m.splitOffset = clamp(m.splitOffset, 0, max(0, len(nav.rows)-height))
}

func (m Model) splitRenderedRowHeight(row splitRow) int {
	if !m.wrapCursorLine {
		return 1
	}
	path := m.currentPath()
	var fileThreads []thread.Thread
	if m.store != nil {
		fileThreads = m.store.ThreadsFor(path)
	}
	pane := diffPane{
		path:            path,
		width:           m.diffWidth(),
		split:           true,
		syntax:          m.syntax,
		contextDim:      m.contextDim,
		wrapCursorLine:  m.wrapCursorLine,
		hideLineNumbers: m.hideLineNumbers,
		syntaxCache:     m.syntaxCache,
		fullWidthHunks:  m.splitFullWidthHunks(),
		session:         m.session,
		markers:         newThreadMarkers(fileThreads),
	}
	line := pane.formatSplitRow(row, pane.syntaxAllowed(m.session.CurrentLineCount()))
	return strings.Count(line, "\n") + 1
}

func splitNavRowTargetLine(row splitRow, delta int) int {
	if row.oldIdx >= 0 && row.newIdx >= 0 {
		if delta < 0 {
			return row.newIdx
		}
		return row.oldIdx
	}
	if row.oldIdx >= 0 {
		return row.oldIdx
	}
	return row.newIdx
}

func splitNavLineForSide(rows []splitRow, rowIdx, delta int, side thread.Side) int {
	if delta == 0 {
		return -1
	}
	for i := rowIdx + delta; i >= 0 && i < len(rows); i += delta {
		if line := splitRowLineForSide(rows[i], side); line >= 0 {
			return line
		}
	}
	return -1
}

func splitRowLineForSide(row splitRow, side thread.Side) int {
	if side == thread.SideOld {
		return row.oldIdx
	}
	if side == thread.SideNew {
		return row.newIdx
	}
	return -1
}

func (p diffPane) Render(height int) string {
	if p.split {
		return p.renderSplit(height)
	}
	style := lipgloss.NewStyle().Width(p.width)
	lineCount := p.session.CurrentLineCount()
	lineIdx := p.session.LineIndex()
	cards := p.inlineThreadCards(height)
	visualRows := lineVisualRows(lineCount, cards)
	baseVisual := visualIndexForLine(visualRows, clamp(p.session.DiffOffset(), 0, max(0, lineCount-1)))
	cursorVisual := visualIndexForLine(visualRows, lineIdx)
	selectedCardEnd := p.selectedLineCardEnd(visualRows, cards)
	start := inlineVisualStart(height, len(visualRows), baseVisual, cursorVisual, selectedCardEnd)
	end := min(len(visualRows), start+height)
	syntaxOK := p.syntaxAllowed(lineCount)
	minLine, maxLine := visibleLineRange(visualRows[start:end])
	intraline := p.intralinePairs(max(0, minLine-intralineContextLines), min(lineCount, maxLine+1+intralineContextLines))

	var rows []string
	for _, vr := range visualRows[start:end] {
		if len(rows) >= height {
			break
		}
		if vr.card {
			rows = append(rows, vr.text)
			continue
		}
		dl := p.session.DisplayLineAt(vr.lineIdx)
		selected := vr.lineIdx == lineIdx
		inRange := p.inActiveRange(vr.lineIdx)
		rangeGlyph := p.rangeGlyph(vr.lineIdx)
		line := p.formatLine(dl, selected, inRange, rangeGlyph, syntaxOK, intraline[vr.lineIdx])
		if !selected {
			if inRange {
				line = padRightStyled(line, p.width, rangeStyle)
			} else {
				line = padRight(line, p.width)
			}
		}
		rows = appendRenderedRows(rows, line, height)
	}
	return style.Render(strings.Join(rows, "\n"))
}

type splitRow struct {
	old        displayLine
	new        displayLine
	oldIdx     int
	newIdx     int
	oldAgainst string
	newAgainst string
	hunk       string
	fullWidth  bool
}

func (p diffPane) renderSplit(height int) string {
	style := lipgloss.NewStyle().Width(p.width)
	lineCount := p.session.CurrentLineCount()
	syntaxOK := p.syntaxAllowed(lineCount)
	rows := p.splitLayoutRows
	if rows == nil {
		rows = p.splitRows(p.session.CurrentLines(), 0)
	}
	lineIdx := p.session.LineIndex()
	selectedRow := splitRowIndexForLine(rows, lineIdx)
	cards := splitThreadCards(p.inlineThreadCards(height), rows)
	visualRows := splitVisualRows(len(rows), cards)
	baseVisual := visualIndexForSplitRow(visualRows, clamp(p.splitOffset, 0, max(0, len(rows)-1)))
	cursorVisual := visualIndexForSplitRow(visualRows, selectedRow)
	selectedCardEnd := p.selectedSplitCardEnd(visualRows, cards, rows)
	start := inlineVisualStart(height, len(visualRows), baseVisual, cursorVisual, selectedCardEnd)
	end := min(len(visualRows), start+height)

	var out []string
	for _, vr := range visualRows[start:end] {
		if len(out) >= height {
			break
		}
		if vr.card {
			out = append(out, vr.text)
			continue
		}
		row := rows[vr.rowIdx]
		line := p.formatSplitRow(row, syntaxOK)
		selected := row.oldIdx == lineIdx || row.newIdx == lineIdx
		inRange := (row.oldIdx >= 0 && p.inActiveRange(row.oldIdx)) || (row.newIdx >= 0 && p.inActiveRange(row.newIdx))
		if !selected {
			if inRange {
				line = padRightStyled(line, p.width, rangeStyle)
			} else {
				line = padRight(line, p.width)
			}
		}
		out = appendRenderedRows(out, line, height)
	}
	return style.Render(strings.Join(out, "\n"))
}

func appendRenderedRows(rows []string, rendered string, maxRows int) []string {
	for _, row := range strings.Split(rendered, "\n") {
		if len(rows) >= maxRows {
			break
		}
		rows = append(rows, row)
	}
	return rows
}

type lineVisualRow struct {
	lineIdx int
	card    bool
	text    string
}

type splitVisualRow struct {
	rowIdx int
	card   bool
	text   string
}

func (p diffPane) inlineThreadCards(height int) map[int][]string {
	if !p.inlineThreads || height < inlineThreadMinScreenRows || p.width < 24 {
		return nil
	}
	cards := make(map[int][]string)
	for _, t := range p.threads {
		anchor := p.threadAnchorIndex(t)
		if anchor < 0 {
			continue
		}
		rows := p.threadCardRows(t)
		if len(rows) > 0 {
			cards[anchor] = append(cards[anchor], rows...)
		}
	}
	return cards
}

func lineVisualRows(lineCount int, cards map[int][]string) []lineVisualRow {
	capacity := lineCount
	for _, cardRows := range cards {
		capacity += len(cardRows)
	}
	rows := make([]lineVisualRow, 0, capacity)
	for i := 0; i < lineCount; i++ {
		rows = append(rows, lineVisualRow{lineIdx: i})
		for _, card := range cards[i] {
			rows = append(rows, lineVisualRow{card: true, text: card})
		}
	}
	return rows
}

func splitVisualRows(rowCount int, cards map[int][]string) []splitVisualRow {
	capacity := rowCount
	for _, cardRows := range cards {
		capacity += len(cardRows)
	}
	rows := make([]splitVisualRow, 0, capacity)
	for i := 0; i < rowCount; i++ {
		rows = append(rows, splitVisualRow{rowIdx: i})
		for _, card := range cards[i] {
			rows = append(rows, splitVisualRow{card: true, text: card})
		}
	}
	return rows
}

func splitThreadCards(lineCards map[int][]string, rows []splitRow) map[int][]string {
	if len(lineCards) == 0 {
		return nil
	}
	cards := make(map[int][]string)
	for lineIdx, cardRows := range lineCards {
		rowIdx := splitRowIndexForLine(rows, lineIdx)
		if rowIdx >= 0 {
			cards[rowIdx] = append(cards[rowIdx], cardRows...)
		}
	}
	return cards
}

func inlineVisualStart(height, rowCount, baseVisual, cursorVisual, selectedCardEnd int) int {
	start := clamp(baseVisual, 0, max(0, rowCount-height))
	if height <= 0 || rowCount == 0 {
		return 0
	}
	if cursorVisual >= 0 {
		if cursorVisual < start {
			start = cursorVisual
		}
		if cursorVisual >= start+height {
			start = cursorVisual - height + 1
		}
	}
	if selectedCardEnd >= 0 && selectedCardEnd >= start+height {
		candidate := selectedCardEnd - height + 1
		if cursorVisual < 0 || candidate <= cursorVisual {
			start = candidate
		}
	}
	return clamp(start, 0, max(0, rowCount-height))
}

func visualIndexForLine(rows []lineVisualRow, lineIdx int) int {
	for i, row := range rows {
		if !row.card && row.lineIdx == lineIdx {
			return i
		}
	}
	return 0
}

func visualIndexForSplitRow(rows []splitVisualRow, rowIdx int) int {
	for i, row := range rows {
		if !row.card && row.rowIdx == rowIdx {
			return i
		}
	}
	return 0
}

func visibleLineRange(rows []lineVisualRow) (int, int) {
	minLine, maxLine := 0, 0
	found := false
	for _, row := range rows {
		if row.card {
			continue
		}
		if !found || row.lineIdx < minLine {
			minLine = row.lineIdx
		}
		if !found || row.lineIdx > maxLine {
			maxLine = row.lineIdx
		}
		found = true
	}
	return minLine, maxLine
}

func (p diffPane) selectedLineCardEnd(rows []lineVisualRow, cards map[int][]string) int {
	if !p.hasSelectedThread {
		return -1
	}
	anchor := p.threadAnchorIndex(p.selectedThread)
	cardRows := cards[anchor]
	if len(cardRows) == 0 {
		return -1
	}
	return visualIndexForLine(rows, anchor) + len(cardRows)
}

func (p diffPane) selectedSplitCardEnd(rows []splitVisualRow, cards map[int][]string, splitRows []splitRow) int {
	if !p.hasSelectedThread {
		return -1
	}
	anchor := splitRowIndexForLine(splitRows, p.threadAnchorIndex(p.selectedThread))
	cardRows := cards[anchor]
	if anchor < 0 || len(cardRows) == 0 {
		return -1
	}
	return visualIndexForSplitRow(rows, anchor) + len(cardRows)
}

func splitRowIndexForLine(rows []splitRow, lineIdx int) int {
	if lineIdx < 0 {
		return -1
	}
	for i, row := range rows {
		if row.oldIdx == lineIdx || row.newIdx == lineIdx {
			return i
		}
	}
	return -1
}

func (p diffPane) threadAnchorIndex(t thread.Thread) int {
	anchor := -1
	for i, dl := range p.session.CurrentLines() {
		if dl.Line != nil && threadtarget.MatchesLine(t, *dl.Line) {
			anchor = i
		}
	}
	return anchor
}

func (p diffPane) threadCardRows(t thread.Thread) []string {
	if p.width < 10 {
		return nil
	}
	cardW := max(10, p.width-2)
	innerW := max(1, cardW-4)
	rows := []string{threadStyle.Render(p.threadCardBorder("╭", p.threadCardTitle(t), "╮", cardW))}
	for _, msg := range inlineThreadRows(t.Messages, innerW) {
		rows = append(rows, p.threadCardBody(msg, cardW))
	}
	rows = append(rows, threadStyle.Render(p.threadCardBorder("╰", "", "╯", cardW)))
	return rows
}

func (p diffPane) threadCardTitle(t thread.Thread) string {
	parts := []string{}
	if t.Source == thread.SourceGitHub {
		parts = append(parts, "github")
	}
	if replies := threadReplyCount(t); replies > 0 {
		parts = append(parts, threadReplyLabel(replies))
	}
	return strings.Join(parts, " · ")
}

func threadReplyCount(t thread.Thread) int {
	return max(0, len(t.Messages)-1)
}

func threadReplyLabel(count int) string {
	if count == 1 {
		return "1 reply"
	}
	return fmt.Sprintf("%d replies", count)
}

func (p diffPane) threadCardBorder(left, text, right string, width int) string {
	innerW := max(0, width-2)
	if text == "" {
		return "  " + left + strings.Repeat("─", innerW) + right
	}
	label := "─ " + text + " "
	if xansi.StringWidth(label) > innerW {
		label = truncate(label, innerW)
	}
	return "  " + left + label + strings.Repeat("─", max(0, innerW-xansi.StringWidth(label))) + right
}

func (p diffPane) threadCardBody(text string, width int) string {
	innerW := max(0, width-4)
	body := text
	if xansi.StringWidth(body) > innerW {
		body = truncate(body, innerW)
	}
	pad := strings.Repeat(" ", max(0, innerW-xansi.StringWidth(body)))
	return dimStyle.Render("  │ ") + body + dimStyle.Render(pad+" │")
}

func inlineThreadRows(messages []thread.Message, width int) []string {
	if width <= 0 {
		return nil
	}
	if len(messages) == 0 {
		return []string{dimStyle.Render("no messages")}
	}
	out := []string{}
	for i, msg := range messages {
		if i > 0 {
			out = append(out, "")
		}
		out = append(out, inlineThreadMessageRows(msg, width)...)
	}
	return out
}

func inlineThreadMessageRows(msg thread.Message, width int) []string {
	bodyRows := inlineThreadMarkdownLines(msg.Body)
	if len(bodyRows) == 0 {
		bodyRows = []string{""}
	}
	author := inlineThreadMessageAuthor(msg)
	if author == "" {
		return wrapInlineThreadRows(bodyRows, width, "", "")
	}
	prefix := threadStyle.Render(author) + dimStyle.Render("  ")
	indent := strings.Repeat(" ", xansi.StringWidth(author)+2)
	return wrapInlineThreadRows(bodyRows, width, prefix, indent)
}

func wrapInlineThreadRows(rows []string, width int, firstPrefix, nextPrefix string) []string {
	out := []string{}
	for i, row := range rows {
		prefix := nextPrefix
		if i == 0 {
			prefix = firstPrefix
		}
		lineW := max(1, width-xansi.StringWidth(prefix))
		wrapped := wrappedANSIParts(row, lineW)
		for j, part := range wrapped {
			linePrefix := prefix
			if j > 0 {
				linePrefix = nextPrefix
			}
			out = append(out, linePrefix+part)
		}
	}
	return out
}

func inlineThreadMarkdownLines(body string) []string {
	body = strings.ReplaceAll(strings.TrimSpace(body), "\r\n", "\n")
	body = strings.ReplaceAll(body, "\r", "")
	if body == "" {
		return []string{""}
	}
	lines := strings.Split(body, "\n")
	out := make([]string, 0, len(lines))
	inFence := false
	for _, raw := range lines {
		line := expandTabs(strings.TrimRight(raw, " \t"))
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			out = append(out, markdownCodeBlockStyle.Render(trimmed))
			inFence = !inFence
			continue
		}
		if inFence {
			out = append(out, markdownCodeBlockStyle.Render(line))
			continue
		}
		if trimmed == "" {
			out = append(out, "")
			continue
		}
		if heading, ok := markdownHeading(line); ok {
			out = append(out, markdownStrongStyle.Render(markdownInline(heading)))
			continue
		}
		if quote, ok := markdownQuote(line); ok {
			out = append(out, markdownQuoteStyle.Render("┃ ")+markdownInline(quote))
			continue
		}
		out = append(out, markdownInline(line))
	}
	return out
}

func markdownHeading(line string) (string, bool) {
	trimmed := strings.TrimLeft(line, " ")
	level := 0
	for level < len(trimmed) && level < 6 && trimmed[level] == '#' {
		level++
	}
	if level == 0 || level >= len(trimmed) || trimmed[level] != ' ' {
		return "", false
	}
	text := strings.TrimSpace(trimmed[level+1:])
	return text, text != ""
}

func markdownQuote(line string) (string, bool) {
	trimmed := strings.TrimLeft(line, " ")
	if !strings.HasPrefix(trimmed, ">") {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(trimmed, ">")), true
}

func markdownInline(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); {
		if strings.HasPrefix(s[i:], "**") {
			if end := strings.Index(s[i+2:], "**"); end >= 0 {
				text := s[i+2 : i+2+end]
				out.WriteString(markdownStrongStyle.Render(text))
				i += 2 + end + 2
				continue
			}
		}
		if s[i] == '`' {
			if end := strings.IndexByte(s[i+1:], '`'); end >= 0 {
				text := s[i+1 : i+1+end]
				out.WriteString(markdownInlineCodeStyle.Render(text))
				i += 1 + end + 1
				continue
			}
		}
		if s[i] == '[' {
			if label, url, n, ok := markdownLink(s[i:]); ok {
				out.WriteString(markdownLinkStyle.Render(label))
				if url != "" {
					out.WriteString(dimStyle.Render(" (" + url + ")"))
				}
				i += n
				continue
			}
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		out.WriteRune(r)
		i += size
	}
	return out.String()
}

func markdownLink(s string) (label, url string, consumed int, ok bool) {
	labelEnd := strings.Index(s, "](")
	if labelEnd <= 0 {
		return "", "", 0, false
	}
	urlStart := labelEnd + 2
	urlEnd := strings.IndexByte(s[urlStart:], ')')
	if urlEnd < 0 {
		return "", "", 0, false
	}
	urlEnd += urlStart
	return s[1:labelEnd], s[urlStart:urlEnd], urlEnd + 1, true
}

func wrappedANSIParts(s string, width int) []string {
	if width <= 0 {
		return []string{""}
	}
	wrapped := strings.TrimSuffix(xansi.Wrap(s, width, " /._-"), "\n")
	if wrapped == "" {
		return []string{""}
	}
	return strings.Split(wrapped, "\n")
}

func inlineThreadMessageAuthor(msg thread.Message) string {
	if msg.AuthorName != "" {
		return msg.AuthorName
	}
	if msg.AuthorLogin != "" {
		return msg.AuthorLogin
	}
	if msg.Actor == thread.ActorHuman {
		return "you"
	}
	if msg.Actor != "" {
		return string(msg.Actor)
	}
	return ""
}

func (p diffPane) intralinePairs(start, end int) map[int]string {
	lines := p.session.LineWindowRange(start, end).Lines
	pairs := make(map[int]string)
	for i := 0; i < len(lines); i++ {
		if lines[i].Line == nil || lines[i].Line.Kind != diff.Delete {
			continue
		}
		delStart := i
		for i < len(lines) && lines[i].Line != nil && lines[i].Line.Kind == diff.Delete {
			i++
		}
		delEnd := i
		addStart := i
		for i < len(lines) && lines[i].Line != nil && lines[i].Line.Kind == diff.Add {
			i++
		}
		addEnd := i
		count := min(delEnd-delStart, addEnd-addStart)
		for j := 0; j < count; j++ {
			_, delBody := diffMarkerBody(lines[delStart+j].Line.Text)
			_, addBody := diffMarkerBody(lines[addStart+j].Line.Text)
			if delBody != addBody {
				pairs[start+delStart+j] = addBody
				pairs[start+addStart+j] = delBody
			}
		}
		i--
	}
	return pairs
}

func (p diffPane) splitRows(lines []displayLine, start int) []splitRow {
	var rows []splitRow
	for i := 0; i < len(lines); i++ {
		dl := lines[i]
		idx := start + i
		if dl.Line == nil {
			rows = append(rows, splitRow{hunk: dl.Text, oldIdx: idx, newIdx: idx})
			continue
		}
		if dl.Line.Kind != diff.Delete {
			if dl.Line.Kind == diff.Add {
				rows = append(rows, splitRow{new: dl, oldIdx: -1, newIdx: idx, fullWidth: p.fullWidthHunks[dl.HunkHeader]})
			} else {
				rows = append(rows, splitRow{old: dl, new: dl, oldIdx: idx, newIdx: idx, fullWidth: p.fullWidthHunks[dl.HunkHeader]})
			}
			continue
		}

		delStart := i
		for i < len(lines) && lines[i].Line != nil && lines[i].Line.Kind == diff.Delete {
			i++
		}
		delEnd := i
		addStart := i
		for i < len(lines) && lines[i].Line != nil && lines[i].Line.Kind == diff.Add {
			i++
		}
		addEnd := i
		for j := 0; j < max(delEnd-delStart, addEnd-addStart); j++ {
			row := splitRow{oldIdx: -1, newIdx: -1, fullWidth: p.fullWidthHunks[dl.HunkHeader]}
			if delStart+j < delEnd {
				row.old = lines[delStart+j]
				row.oldIdx = start + delStart + j
			}
			if addStart+j < addEnd {
				row.new = lines[addStart+j]
				row.newIdx = start + addStart + j
			}
			if row.old.Line != nil && row.new.Line != nil {
				_, oldBody := diffMarkerBody(row.old.Line.Text)
				_, newBody := diffMarkerBody(row.new.Line.Text)
				if oldBody != newBody {
					row.oldAgainst = newBody
					row.newAgainst = oldBody
				}
			}
			rows = append(rows, row)
		}
		i--
	}
	return rows
}

func (p diffPane) syntaxAllowed(lineCount int) bool {
	limit := syntaxMaxFileLines
	if p.split {
		limit = splitSyntaxMaxFileLines
	}
	return p.syntax && lineCount <= limit && lexers.Match(p.path) != nil
}

func (p diffPane) formatLine(dl displayLine, selected, inRange bool, rangeGlyph string, syntaxOK bool, intralineAgainst string) string {
	if dl.Line == nil {
		text := railPrefix(rangeGlyph) + dl.Text
		if selected {
			if p.wrapCursorLine && xansi.StringWidth(text) > p.width {
				return wrapStyledPadded(text, p.width, selectedHunkStyle, selectedStyle)
			}
			return padRightStyled(selectedHunkStyle.Render(truncate(text, p.width)), p.width, selectedStyle)
		}
		text = truncate(text, p.width)
		if inRange {
			return rangeHunkStyle.Render(text)
		}
		return hunkStyle.Render(text)
	}
	l := *dl.Line
	marker := p.markers.markerForLine(l)
	if marker == "" {
		marker = " "
	}
	return p.formatUnifiedLine(l, marker, selected, inRange, rangeGlyph, syntaxOK, intralineAgainst)
}

func (p diffPane) formatSplitRow(row splitRow, syntaxOK bool) string {
	if row.hunk != "" {
		text := truncate(row.hunk, p.width)
		if row.oldIdx == p.session.LineIndex() || row.newIdx == p.session.LineIndex() {
			return padRightStyled(selectedHunkStyle.Render(text), p.width, selectedStyle)
		}
		if (row.oldIdx >= 0 && p.inActiveRange(row.oldIdx)) || (row.newIdx >= 0 && p.inActiveRange(row.newIdx)) {
			return rangeHunkStyle.Render(text)
		}
		return hunkStyle.Render(text)
	}
	oldSelected, newSelected, oldInRange, newInRange := p.splitRowSideState(row)
	selected := oldSelected || newSelected
	inRange := oldInRange || newInRange
	glyphIdx := row.oldIdx
	if p.session.RangeActive() {
		glyphIdx = splitRowLineForSide(row, p.session.RangeSide())
	} else if glyphIdx < 0 {
		glyphIdx = row.newIdx
	}
	if selected {
		glyphIdx = p.session.LineIndex()
	}
	rangeGlyph := p.rangeGlyph(glyphIdx)
	marker := p.splitRowMarker(row)
	prefix := railCell(railGlyph(marker, rangeGlyph), selected, inRange) + gutterView(" ", selected, inRange)
	if row.fullWidth {
		return p.formatFullWidthSplitRow(row, prefix, marker, rangeGlyph, selected, inRange, syntaxOK)
	}
	fixed := 2 + 3
	if !p.hideLineNumbers {
		fixed += lineNoWidth + 1 + lineNoWidth + 1
	}
	leftW := max(1, (p.width-fixed)/2)
	rightW := max(1, p.width-fixed-leftW)
	oldPrefix := ""
	newPrefix := ""
	if !p.hideLineNumbers {
		oldNo, oldKind := splitLineNoAndKind(row.old.Line, thread.SideOld)
		newNo, newKind := splitLineNoAndKind(row.new.Line, thread.SideNew)
		oldPrefix = p.lineNoView(oldNo, oldKind, oldSelected, oldInRange) + gutterView(" ", oldSelected, oldInRange)
		newPrefix = p.lineNoView(newNo, newKind, newSelected, newInRange) + gutterView(" ", newSelected, newInRange)
	}
	if selected && p.wrapCursorLine && p.splitRowNeedsWrap(row, leftW, rightW, syntaxOK) {
		return p.formatWrappedSplitRow(row, marker, rangeGlyph, selected, inRange, oldSelected, newSelected, oldInRange, newInRange, oldPrefix, newPrefix, leftW, rightW, syntaxOK)
	}
	rest := oldPrefix + p.splitCellText(row.old.Line, thread.SideOld, leftW, oldSelected, oldInRange, syntaxOK, row.oldAgainst) + gutterView(" │ ", selected, inRange) + newPrefix + p.splitCellText(row.new.Line, thread.SideNew, rightW, newSelected, newInRange, syntaxOK, row.newAgainst)
	line := prefix + rest
	if selected {
		line = railCell(railGlyph(marker, rangeGlyph), true, inRange) + selectedStyle.Render(" ") + rest
		return padRightStyled(truncate(line, p.width), p.width, selectedStyle)
	}
	return truncate(line, p.width)
}

func (p diffPane) splitRowSideState(row splitRow) (oldSelected, newSelected, oldInRange, newInRange bool) {
	current := p.session.LineIndex()
	if p.session.RangeActive() {
		side := p.session.RangeSide()
		oldSelected = side == thread.SideOld && row.oldIdx == current
		newSelected = side == thread.SideNew && row.newIdx == current
		oldInRange = side == thread.SideOld && row.oldIdx >= 0 && p.inActiveRange(row.oldIdx)
		newInRange = side == thread.SideNew && row.newIdx >= 0 && p.inActiveRange(row.newIdx)
		return oldSelected, newSelected, oldInRange, newInRange
	}
	oldSelected = row.oldIdx == current
	newSelected = row.newIdx == current
	return oldSelected, newSelected, false, false
}

func (p diffPane) formatFullWidthSplitRow(row splitRow, prefix, marker, rangeGlyph string, selected, inRange, syntaxOK bool) string {
	line := row.new.Line
	side := thread.SideNew
	if line == nil {
		line = row.old.Line
		side = thread.SideOld
	}
	if line == nil {
		return ""
	}
	rest := ""
	kind := splitLineKind(line, side)
	if !p.hideLineNumbers {
		lineNo, _ := splitLineNoAndKind(line, side)
		rest += p.lineNoView(lineNo, kind, selected, inRange) + gutterView(" ", selected, inRange)
	}
	fixed := 2
	if !p.hideLineNumbers {
		fixed += lineNoWidth + 1
	}
	cellW := max(1, p.width-fixed)
	if selected && p.wrapCursorLine {
		diffSign, body := diffMarkerBody(line.Text)
		linePrefix := railCell(railGlyph(marker, rangeGlyph), true, inRange) + selectedStyle.Render(" ") + rest + diffSignView(diffSign, kind, true, inRange) + gutterView(" ", true, inRange)
		bodyW := max(1, p.width-xansi.StringWidth(linePrefix))
		if xansi.StringWidth(body) > bodyW {
			return p.wrapSelectedBody(linePrefix, kind, body, true, inRange, syntaxOK, bodyW, "")
		}
	}
	rest += p.splitCellText(line, side, cellW, selected, inRange, syntaxOK, "")
	if selected {
		line := railCell(railGlyph(marker, rangeGlyph), true, inRange) + selectedStyle.Render(" ") + rest
		return padRightStyled(truncate(line, p.width), p.width, selectedStyle)
	}
	return truncate(prefix+rest, p.width)
}

func (p diffPane) splitRowNeedsWrap(row splitRow, leftW, rightW int, syntaxOK bool) bool {
	return p.splitLineNeedsWrap(row.old.Line, thread.SideOld, leftW, syntaxOK, row.oldAgainst) ||
		p.splitLineNeedsWrap(row.new.Line, thread.SideNew, rightW, syntaxOK, row.newAgainst)
}

func (p diffPane) splitLineNeedsWrap(line *diff.Line, side thread.Side, width int, syntaxOK bool, intralineAgainst string) bool {
	if line == nil || width <= 2 {
		return false
	}
	_, body := diffMarkerBody(line.Text)
	bodyW := max(1, width-2)
	return xansi.StringWidth(body) > bodyW
}

func (p diffPane) formatWrappedSplitRow(row splitRow, marker, rangeGlyph string, selected, inRange, oldSelected, newSelected, oldInRange, newInRange bool, oldPrefix, newPrefix string, leftW, rightW int, syntaxOK bool) string {
	leftRows := p.splitCellTextRows(row.old.Line, thread.SideOld, leftW, oldSelected, oldInRange, syntaxOK, row.oldAgainst)
	rightRows := p.splitCellTextRows(row.new.Line, thread.SideNew, rightW, newSelected, newInRange, syntaxOK, row.newAgainst)
	rowCount := max(len(leftRows), len(rightRows))
	if rowCount == 0 {
		return ""
	}
	rows := make([]string, 0, rowCount)
	for i := 0; i < rowCount; i++ {
		linePrefix := selectedStyle.Render(strings.Repeat(" ", 2))
		leftNo := selectedStyle.Render(strings.Repeat(" ", xansi.StringWidth(oldPrefix)))
		newNo := selectedStyle.Render(strings.Repeat(" ", xansi.StringWidth(newPrefix)))
		if i == 0 {
			linePrefix = railCell(railGlyph(marker, rangeGlyph), true, inRange) + selectedStyle.Render(" ")
			leftNo = oldPrefix
			newNo = newPrefix
		}
		left := selectedStyle.Render(strings.Repeat(" ", leftW))
		if i < len(leftRows) {
			left = leftRows[i]
		}
		right := selectedStyle.Render(strings.Repeat(" ", rightW))
		if i < len(rightRows) {
			right = rightRows[i]
		}
		line := linePrefix + leftNo + left + gutterView(" │ ", selected, inRange) + newNo + right
		rows = append(rows, padRightStyled(truncate(line, p.width), p.width, selectedStyle))
	}
	return strings.Join(rows, "\n")
}

func (p diffPane) splitCellTextRows(line *diff.Line, side thread.Side, width int, selected, inRange, syntaxOK bool, intralineAgainst string) []string {
	if width <= 0 {
		return nil
	}
	if line == nil {
		return []string{selectedStyle.Render(strings.Repeat(" ", width))}
	}
	kind := splitLineKind(line, side)
	marker, body := diffMarkerBody(line.Text)
	bodyW := max(1, width-2)
	parts := p.wrappedBodyParts(kind, body, selected, inRange, syntaxOK, bodyW, intralineAgainst)
	rows := make([]string, 0, len(parts))
	for i, part := range parts {
		sign := diffSignView(marker, kind, selected, inRange)
		if i > 0 {
			sign = selectedStyle.Render(" ")
		}
		text := sign + gutterView(" ", selected, inRange) + part
		rows = append(rows, padRightStyled(text, width, splitCellPadStyle(selected, inRange)))
	}
	return rows
}

func splitLineKind(line *diff.Line, side thread.Side) diff.Kind {
	if line == nil {
		return diff.Context
	}
	if side == thread.SideOld && line.Kind == diff.Delete {
		return diff.Delete
	}
	if side == thread.SideNew && line.Kind == diff.Add {
		return diff.Add
	}
	return diff.Context
}

func splitLineNoAndKind(line *diff.Line, side thread.Side) (string, diff.Kind) {
	if line == nil {
		return lineNoText(0), diff.Context
	}
	if side == thread.SideOld {
		kind := diff.Context
		if line.Kind == diff.Delete {
			kind = diff.Delete
		}
		return lineNoText(line.OldNo), kind
	}
	kind := diff.Context
	if line.Kind == diff.Add {
		kind = diff.Add
	}
	return lineNoText(line.NewNo), kind
}

func (p diffPane) splitCellText(line *diff.Line, side thread.Side, width int, selected, inRange, syntaxOK bool, intralineAgainst string) string {
	if line == nil || width <= 0 {
		return gutterView(strings.Repeat(" ", max(0, width)), selected, inRange)
	}
	kind := diff.Context
	if side == thread.SideOld && line.Kind == diff.Delete {
		kind = diff.Delete
	}
	if side == thread.SideNew && line.Kind == diff.Add {
		kind = diff.Add
	}
	marker, body := diffMarkerBody(line.Text)
	bodyW := max(0, width-2)
	body = truncate(body, bodyW)
	bodyView := p.syntaxBodyView(kind, body, selected, inRange, syntaxOK)
	if intralineAgainst != "" && (kind == diff.Add || kind == diff.Delete) {
		bodyView = p.intralineTextView(kind, body, intralineAgainst, selected, inRange, syntaxOK)
	}
	text := diffSignView(marker, kind, selected, inRange) + gutterView(" ", selected, inRange) + bodyView
	return padRightStyled(text, width, splitCellPadStyle(selected, inRange))
}

func splitCellPadStyle(selected, inRange bool) lipgloss.Style {
	if selected {
		return selectedStyle
	}
	if inRange {
		return rangeStyle
	}
	return lipgloss.NewStyle()
}

func (p diffPane) splitRowMarker(row splitRow) string {
	for _, line := range []*diff.Line{row.old.Line, row.new.Line} {
		if line == nil {
			continue
		}
		if marker := p.markers.markerForLine(*line); marker != "" {
			return marker
		}
	}
	return " "
}

func (p diffPane) formatUnifiedLine(l diff.Line, marker string, selected, inRange bool, rangeGlyph string, syntaxOK bool, intralineAgainst string) string {
	diffSign, body := diffMarkerBody(l.Text)
	prefix := railCell(railGlyph(marker, rangeGlyph), selected, inRange) + gutterView(" ", selected, inRange)
	bodyView := p.diffTextView(l.Kind, body, selected, inRange, syntaxOK)
	if intralineAgainst != "" && (l.Kind == diff.Add || l.Kind == diff.Delete) {
		bodyView = p.intralineTextView(l.Kind, body, intralineAgainst, selected, inRange, syntaxOK)
	}
	restPrefix := ""
	if !p.hideLineNumbers {
		oldNo := lineNoText(l.OldNo)
		newNo := lineNoText(l.NewNo)
		restPrefix += p.lineNoView(oldNo, l.Kind, selected, inRange) + gutterView(" ", selected, inRange) + p.lineNoView(newNo, l.Kind, selected, inRange) + gutterView(" ", selected, inRange)
	}
	restPrefix += diffSignView(diffSign, l.Kind, selected, inRange) + gutterView(" ", selected, inRange)
	if selected {
		linePrefix := railCell(railGlyph(marker, rangeGlyph), true, inRange) + selectedStyle.Render(" ") + restPrefix
		bodyW := max(1, p.width-xansi.StringWidth(linePrefix))
		if p.wrapCursorLine && xansi.StringWidth(body) > bodyW {
			return p.wrapSelectedBody(linePrefix, l.Kind, body, true, inRange, syntaxOK, bodyW, intralineAgainst)
		}
		return padRightStyled(truncate(linePrefix+bodyView, p.width), p.width, selectedStyle)
	}
	return truncate(prefix+restPrefix+bodyView, p.width)
}

func (p diffPane) wrapSelectedLine(prefix, body string) string {
	prefixW := xansi.StringWidth(prefix)
	if prefixW >= p.width {
		return wrapPadded(prefix+body, p.width, selectedStyle)
	}
	bodyW := max(1, p.width-prefixW)
	return p.wrapSelectedBody(prefix, diff.Context, body, true, false, false, bodyW, "")
}

func (p diffPane) wrapSelectedBody(prefix string, kind diff.Kind, body string, selected, inRange, syntaxOK bool, bodyW int, intralineAgainst string) string {
	prefixW := xansi.StringWidth(prefix)
	parts := p.wrappedBodyParts(kind, body, selected, inRange, syntaxOK, bodyW, intralineAgainst)
	rows := make([]string, 0, len(parts))
	for i, part := range parts {
		linePrefix := prefix
		if i > 0 {
			linePrefix = selectedStyle.Render(strings.Repeat(" ", prefixW))
		}
		rows = append(rows, padRightStyled(linePrefix+part, p.width, selectedStyle))
	}
	return strings.Join(rows, "\n")
}

func wrapPadded(s string, width int, style lipgloss.Style) string {
	parts := wrappedParts(s, width)
	rows := make([]string, 0, len(parts))
	for _, part := range parts {
		rows = append(rows, padRightStyled(part, width, style))
	}
	return strings.Join(rows, "\n")
}

func wrappedParts(s string, width int) []string {
	wrapped := strings.TrimSuffix(xansi.Wrap(s, width, " /._"), "\n")
	if wrapped == "" {
		return []string{""}
	}
	return strings.Split(wrapped, "\n")
}

func (p diffPane) wrappedBodyParts(kind diff.Kind, body string, selected, inRange, syntaxOK bool, width int, intralineAgainst string) []string {
	body = expandTabs(body)
	intralineAgainst = expandTabs(intralineAgainst)
	rendered := p.syntaxBodyView(kind, body, selected, inRange, syntaxOK)
	plainParts := wrappedParts(body, width)
	highlightStart, highlightEnd, bg, highlight := intralineHighlight(kind, body, intralineAgainst)
	parts := make([]string, 0, len(plainParts))
	pos := 0
	for _, part := range plainParts {
		idx := strings.Index(body[pos:], part)
		if idx < 0 {
			idx = 0
		}
		pos += idx
		partStart := xansi.StringWidth(body[:pos])
		pos += len(part)
		partEnd := xansi.StringWidth(body[:pos])
		renderedPart := ansiSlice(rendered, partStart, partEnd)
		if highlight && highlightStart < partEnd && highlightEnd > partStart {
			renderedPart = withANSIBackgroundSpan(renderedPart, max(0, highlightStart-partStart), min(partEnd, highlightEnd)-partStart, bg, baseBackground(selected, inRange))
		}
		parts = append(parts, renderedPart)
	}
	return parts
}

type sgrState struct {
	bold bool
	dim  bool
	fg   string
	bg   string
}

func (s sgrState) seq() string {
	params := []string{}
	if s.bold {
		params = append(params, "1")
	}
	if s.dim {
		params = append(params, "2")
	}
	if s.fg != "" {
		params = append(params, "38", "5", s.fg)
	}
	if s.bg != "" {
		params = append(params, "48", "5", s.bg)
	}
	if len(params) == 0 {
		return ""
	}
	return "\x1b[" + strings.Join(params, ";") + "m"
}

func (s *sgrState) apply(seq string) {
	if !strings.HasPrefix(seq, "\x1b[") || !strings.HasSuffix(seq, "m") {
		return
	}
	body := strings.TrimSuffix(strings.TrimPrefix(seq, "\x1b["), "m")
	if body == "" {
		body = "0"
	}
	parts := strings.Split(body, ";")
	for i := 0; i < len(parts); i++ {
		switch parts[i] {
		case "0":
			*s = sgrState{}
		case "1":
			s.bold = true
		case "2":
			s.dim = true
		case "22":
			s.bold = false
			s.dim = false
		case "38":
			if i+2 < len(parts) && parts[i+1] == "5" {
				s.fg = parts[i+2]
				i += 2
			}
		case "39":
			s.fg = ""
		case "48":
			if i+2 < len(parts) && parts[i+1] == "5" {
				s.bg = parts[i+2]
				i += 2
			}
		case "49":
			s.bg = ""
		}
	}
}

func ansiSlice(s string, start, end int) string {
	if start >= end {
		return ""
	}
	var state sgrState
	var out strings.Builder
	visible := 0
	for i := 0; i < len(s); {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			if j < len(s) {
				j++
			}
			seq := s[i:j]
			state.apply(seq)
			if visible >= start && visible < end {
				out.WriteString(seq)
			}
			i = j
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if visible >= start && visible < end {
			if out.Len() == 0 {
				out.WriteString(state.seq())
			}
			out.WriteRune(r)
		}
		i += size
		visible += xansi.StringWidth(string(r))
		if visible >= end {
			break
		}
	}
	if out.Len() == 0 {
		return ""
	}
	return out.String() + "\x1b[0m"
}

func expandTabs(s string) string {
	return strings.ReplaceAll(s, "\t", "    ")
}

func wrapStyledPadded(s string, width int, textStyle, padStyle lipgloss.Style) string {
	parts := wrappedParts(s, width)
	rows := make([]string, 0, len(parts))
	for _, part := range parts {
		rows = append(rows, padRightStyled(withANSIBackground(textStyle.Render(part), selectedBg), width, padStyle))
	}
	return strings.Join(rows, "\n")
}

func lineNoText(n int) string {
	if n <= 0 {
		return strings.Repeat(" ", lineNoWidth)
	}
	return fmt.Sprintf("%*d", lineNoWidth, n)
}

func diffSignView(marker string, kind diff.Kind, selected, inRange bool) string {
	if marker == "" {
		marker = " "
	}
	return diffTextColor(kind, marker, selected, inRange)
}

func (p diffPane) lineNoView(s string, kind diff.Kind, selected, inRange bool) string {
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

func (p diffPane) diffTextView(kind diff.Kind, s string, selected, inRange, syntaxOK bool) string {
	if !syntaxOK || kind == diff.Meta {
		return diffTextColor(kind, s, selected, inRange)
	}
	if selected {
		return withANSIBackground(p.syntaxView(s), selectedBg)
	}
	if inRange {
		return withANSIBackground(p.syntaxView(s), rangeBg)
	}
	if kind == diff.Context && p.contextDim {
		return withANSIDim(p.syntaxView(s))
	}
	return p.syntaxView(s)
}

func (p diffPane) intralineTextView(kind diff.Kind, body, against string, selected, inRange, syntaxOK bool) string {
	base := p.diffTextView(kind, body, selected, inRange, syntaxOK)
	start, end, bg, ok := intralineHighlight(kind, body, against)
	if !ok {
		return base
	}
	return withANSIBackgroundSpan(base, start, end, bg, baseBackground(selected, inRange))
}

func intralineHighlight(kind diff.Kind, body, against string) (int, int, lipgloss.Color, bool) {
	if against == "" || (kind != diff.Add && kind != diff.Delete) {
		return 0, 0, "", false
	}
	start, end := changedSpan(body, against)
	if start == end {
		return 0, 0, "", false
	}
	runes := []rune(body)
	if strings.TrimSpace(string(runes[:start])) == "" && strings.TrimSpace(string(runes[end:])) == "" {
		return 0, 0, "", false
	}
	bg := addChangedBg
	if kind == diff.Delete {
		bg = deleteChangedBg
	}
	return xansi.StringWidth(string(runes[:start])), xansi.StringWidth(string(runes[:end])), bg, true
}

func changedSpan(a, b string) (int, int) {
	ar, br := []rune(a), []rune(b)
	start := 0
	for start < len(ar) && start < len(br) && ar[start] == br[start] {
		start++
	}
	endA, endB := len(ar), len(br)
	for endA > start && endB > start && ar[endA-1] == br[endB-1] {
		endA--
		endB--
	}
	return expandWordSpan(ar, start, endA)
}

func expandWordSpan(runes []rune, start, end int) (int, int) {
	if start == end || len(runes) == 0 {
		return start, end
	}
	for start > 0 && start < len(runes) && isWordRune(runes[start-1]) && isWordRune(runes[start]) {
		start--
	}
	for end > start && end < len(runes) && isWordRune(runes[end-1]) && isWordRune(runes[end]) {
		end++
	}
	return start, end
}

func isWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func (p diffPane) splitTextView(kind diff.Kind, side thread.Side, s string, selected, inRange, syntaxOK bool) string {
	if kind == diff.Delete && side == thread.SideOld {
		return p.syntaxBodyView(kind, s, selected, inRange, syntaxOK)
	}
	if kind == diff.Add && side == thread.SideNew {
		return p.syntaxBodyView(kind, s, selected, inRange, syntaxOK)
	}
	if selected {
		if syntaxOK {
			return withANSIBackground(p.syntaxView(s), selectedBg)
		}
		return selectedStyle.Render(s)
	}
	if inRange {
		if syntaxOK {
			return withANSIBackground(p.syntaxView(s), rangeBg)
		}
		return rangeStyle.Render(s)
	}
	if syntaxOK {
		return p.syntaxView(s)
	}
	return s
}

func (p diffPane) syntaxBodyView(kind diff.Kind, s string, selected, inRange, syntaxOK bool) string {
	if !syntaxOK {
		return diffTextColor(kind, s, selected, inRange)
	}
	if selected {
		return withANSIBackground(p.syntaxView(s), selectedBg)
	}
	if inRange {
		return withANSIBackground(p.syntaxView(s), rangeBg)
	}
	if kind == diff.Context && p.contextDim {
		return withANSIDim(p.syntaxView(s))
	}
	return p.syntaxView(s)
}

func withANSIDim(s string) string {
	if s == "" {
		return s
	}
	dim := "\x1b[2m"
	return dim + strings.ReplaceAll(s, "\x1b[0m", "\x1b[0m"+dim) + "\x1b[0m"
}

func withANSIBackground(s string, color lipgloss.Color) string {
	if s == "" {
		return s
	}
	bg := ansiBackground(color)
	return bg + strings.ReplaceAll(s, "\x1b[0m", "\x1b[0m"+bg) + "\x1b[0m"
}

func withANSIBackgroundSpan(s string, start, end int, color lipgloss.Color, restore lipgloss.Color) string {
	if s == "" || start >= end {
		return s
	}
	highlight := ansiBackground(color)
	restoreBg := ansiBackgroundOff()
	if restore != "" {
		restoreBg = ansiBackground(restore)
	}
	var out strings.Builder
	out.Grow(len(s) + len(highlight) + len(restoreBg))
	visible := 0
	active := false
	for i := 0; i < len(s); {
		if !active && visible == start {
			out.WriteString(highlight)
			active = true
		}
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			if j < len(s) {
				j++
			}
			out.WriteString(s[i:j])
			if active {
				out.WriteString(highlight)
			}
			i = j
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		out.WriteRune(r)
		i += size
		visible += xansi.StringWidth(string(r))
		if active && visible >= end {
			out.WriteString(restoreBg)
			active = false
		}
	}
	if active {
		out.WriteString(restoreBg)
	}
	return out.String()
}

func ansiBackground(color lipgloss.Color) string {
	return "\x1b[48;5;" + string(color) + "m"
}

func ansiBackgroundOff() string { return "\x1b[49m" }

func baseBackground(selected, inRange bool) lipgloss.Color {
	if selected {
		return selectedBg
	}
	if inRange {
		return rangeBg
	}
	return ""
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

func (p diffPane) syntaxView(s string) string {
	if strings.TrimSpace(s) == "" || xansi.StringWidth(s) > syntaxMaxLineWidth {
		return s
	}
	body := strings.TrimRight(s, " \t")
	trail := s[len(body):]
	key := p.path + "\x00" + body
	if p.syntaxCache != nil {
		if cached, ok := p.syntaxCache[key]; ok {
			return cached + trail
		}
		if len(p.syntaxCache) >= syntaxCacheMaxEntries {
			clear(p.syntaxCache)
		}
	}
	lexer := lexers.Match(p.path)
	if lexer == nil {
		return s
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
	if p.syntaxCache != nil {
		p.syntaxCache[key] = highlighted
	}
	return highlighted + trail
}

func (p diffPane) rangeGlyph(idx int) string {
	return p.session.RangeGlyph(idx)
}

func railGlyph(threadMarker, rangeGlyph string) string {
	if strings.TrimSpace(rangeGlyph) != "" {
		return rangeGlyph
	}
	return threadMarker
}

func railCell(glyph string, selected, inRange bool) string {
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
		return selectedThreadStyle.Render(glyph)
	}
	if inRange {
		return rangeThreadStyle.Render(glyph)
	}
	return threadStyle.Render(glyph)
}

func railPrefix(glyph string) string {
	if strings.TrimSpace(glyph) == "" {
		return ""
	}
	return glyph + " "
}

func (p diffPane) inActiveRange(idx int) bool {
	return p.session.InActiveRange(idx)
}
