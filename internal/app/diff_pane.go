package app

import (
	"bytes"
	"fmt"
	"strings"

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
				glyph = "●"
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
	path            string
	width           int
	split           bool
	syntax          bool
	contextDim      bool
	wrapCursorLine  bool
	hideLineNumbers bool
	syntaxCache     map[string]string
	fullWidthHunks  map[string]bool
	splitLayoutRows []splitRow
	splitOffset     int
	session         review.Session
	markers         threadMarkers
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
	return diffPane{
		path:            path,
		width:           width,
		split:           m.split,
		syntax:          m.syntax,
		contextDim:      m.contextDim,
		wrapCursorLine:  m.wrapCursorLine,
		hideLineNumbers: m.hideLineNumbers,
		syntaxCache:     m.syntaxCache,
		fullWidthHunks:  fullWidthHunks,
		splitLayoutRows: splitRows,
		splitOffset:     m.splitOffset,
		session:         m.session,
		markers:         newThreadMarkers(fileThreads),
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
	targetRow := clamp(rowIdx+delta, 0, len(nav.rows)-1)
	targetLine := splitNavRowTargetLine(nav.rows[targetRow], delta)
	if targetLine < 0 || targetLine == current {
		return
	}
	m.session.JumpToIndex(m.session.FileIndex(), targetLine, m.bodyHeight())
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

func (p diffPane) Render(height int) string {
	if p.split {
		return p.renderSplit(height)
	}
	style := lipgloss.NewStyle().Width(p.width)
	window := p.session.LineWindow(height)
	lineCount := window.LineCount
	lineIdx := window.LineIndex
	start := window.Start
	lines := window.Lines
	syntaxOK := p.syntaxAllowed(lineCount)
	intraline := p.intralinePairs(max(0, window.Start-intralineContextLines), min(lineCount, window.End+intralineContextLines))

	var rows []string
	for offset, dl := range lines {
		if len(rows) >= height {
			break
		}
		i := start + offset
		selected := i == lineIdx
		inRange := p.inActiveRange(i)
		rangeGlyph := p.rangeGlyph(i)
		line := p.formatLine(dl, selected, inRange, rangeGlyph, syntaxOK, intraline[i])
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
	start := clamp(p.splitOffset, 0, max(0, len(rows)-height))
	end := min(len(rows), start+height)
	rows = rows[start:end]

	var out []string
	lineIdx := p.session.LineIndex()
	for _, row := range rows {
		if len(out) >= height {
			break
		}
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
	selected := row.oldIdx == p.session.LineIndex() || row.newIdx == p.session.LineIndex()
	inRange := (row.oldIdx >= 0 && p.inActiveRange(row.oldIdx)) || (row.newIdx >= 0 && p.inActiveRange(row.newIdx))
	glyphIdx := row.oldIdx
	if glyphIdx < 0 {
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
		oldPrefix = p.lineNoView(oldNo, oldKind, selected, inRange) + gutterView(" ", selected, inRange)
		newPrefix = p.lineNoView(newNo, newKind, selected, inRange) + gutterView(" ", selected, inRange)
	}
	if selected && p.wrapCursorLine && p.splitRowNeedsWrap(row, leftW, rightW, syntaxOK) {
		return p.formatWrappedSplitRow(row, marker, rangeGlyph, inRange, oldPrefix, newPrefix, leftW, rightW, syntaxOK)
	}
	rest := oldPrefix + p.splitCellText(row.old.Line, thread.SideOld, leftW, selected, inRange, syntaxOK, row.oldAgainst) + gutterView(" │ ", selected, inRange) + newPrefix + p.splitCellText(row.new.Line, thread.SideNew, rightW, selected, inRange, syntaxOK, row.newAgainst)
	line := prefix + rest
	if selected {
		line = railCell(railGlyph(marker, rangeGlyph), true, inRange) + selectedStyle.Render(" ") + rest
		return padRightStyled(truncate(line, p.width), p.width, selectedStyle)
	}
	return truncate(line, p.width)
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
			return p.wrapSelectedBody(linePrefix, kind, body, true, inRange, syntaxOK, bodyW)
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

func (p diffPane) formatWrappedSplitRow(row splitRow, marker, rangeGlyph string, inRange bool, oldPrefix, newPrefix string, leftW, rightW int, syntaxOK bool) string {
	leftRows := p.splitCellTextRows(row.old.Line, thread.SideOld, leftW, true, inRange, syntaxOK, row.oldAgainst)
	rightRows := p.splitCellTextRows(row.new.Line, thread.SideNew, rightW, true, inRange, syntaxOK, row.newAgainst)
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
		line := linePrefix + leftNo + left + gutterView(" │ ", true, inRange) + newNo + right
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
	parts := wrappedParts(body, bodyW)
	rows := make([]string, 0, len(parts))
	for i, part := range parts {
		sign := diffSignView(marker, kind, selected, inRange)
		if i > 0 {
			sign = selectedStyle.Render(" ")
		}
		bodyPart := p.syntaxBodyView(kind, part, selected, inRange, syntaxOK)
		if intralineAgainst != "" && (kind == diff.Add || kind == diff.Delete) {
			bodyPart = p.intralineTextView(kind, part, intralineAgainst, selected, inRange, syntaxOK)
		}
		text := sign + gutterView(" ", selected, inRange) + bodyPart
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
			return p.wrapSelectedBody(linePrefix, l.Kind, body, true, inRange, syntaxOK, bodyW)
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
	return p.wrapSelectedBody(prefix, diff.Context, body, true, false, false, bodyW)
}

func (p diffPane) wrapSelectedBody(prefix string, kind diff.Kind, body string, selected, inRange, syntaxOK bool, bodyW int) string {
	prefixW := xansi.StringWidth(prefix)
	parts := wrappedParts(body, bodyW)
	rows := make([]string, 0, len(parts))
	for i, part := range parts {
		linePrefix := prefix
		if i > 0 {
			linePrefix = selectedStyle.Render(strings.Repeat(" ", prefixW))
		}
		bodyPart := p.syntaxBodyView(kind, part, selected, inRange, syntaxOK)
		rows = append(rows, padRightStyled(linePrefix+bodyPart, p.width, selectedStyle))
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
	start, end := changedSpan(body, against)
	if start == end {
		return p.diffTextView(kind, body, selected, inRange, syntaxOK)
	}
	runes := []rune(body)
	prefix := string(runes[:start])
	changed := string(runes[start:end])
	suffix := string(runes[end:])
	if strings.TrimSpace(prefix) == "" && strings.TrimSpace(suffix) == "" {
		return p.diffTextView(kind, body, selected, inRange, syntaxOK)
	}
	bg := addChangedBg
	if kind == diff.Delete {
		bg = deleteChangedBg
	}
	return p.diffTextView(kind, prefix, selected, inRange, syntaxOK) +
		withANSIBackground(p.diffTextView(kind, changed, false, false, syntaxOK), bg) +
		p.diffTextView(kind, suffix, selected, inRange, syntaxOK)
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
	return start, endA
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
