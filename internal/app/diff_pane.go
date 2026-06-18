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
	"github.com/owenps/tdiff/internal/annotate"
	"github.com/owenps/tdiff/internal/annotations"
	"github.com/owenps/tdiff/internal/diff"
	"github.com/owenps/tdiff/internal/review"
)

type diffPane struct {
	path            string
	width           int
	split           bool
	syntax          bool
	contextDim      bool
	wrapCursorLine  bool
	syntaxCache     map[string]string
	cursor          review.Cursor
	workflow        annotations.Workflow
	fileAnnotations []annotate.Annotation
}

func (m Model) renderDiff(height int) string {
	width := max(40, m.width-sidebarWidth-2)
	if m.hideSidebar {
		width = max(40, m.width)
	}
	return m.diffPane(width).Render(height)
}

func (m Model) diffPane(width int) diffPane {
	path := m.currentPath()
	var fileAnnotations []annotate.Annotation
	if m.store != nil {
		fileAnnotations = m.store.AnnotationsFor(path)
	}
	return diffPane{
		path:            path,
		width:           width,
		split:           m.split,
		syntax:          m.syntax,
		contextDim:      m.contextDim,
		wrapCursorLine:  m.wrapCursorLine,
		syntaxCache:     m.syntaxCache,
		cursor:          m.session.Cursor(),
		workflow:        m.annotations,
		fileAnnotations: fileAnnotations,
	}
}

func (m Model) syntaxAllowed(lineCount int) bool {
	return m.diffPane(0).syntaxAllowed(lineCount)
}

func (p diffPane) Render(height int) string {
	if p.split {
		return p.renderSplit(height)
	}
	style := lipgloss.NewStyle().Width(p.width)
	lineCount := p.cursor.CurrentLineCount()
	lineIdx := p.cursor.LineIndex()
	start := clamp(p.cursor.DiffOffset(), 0, max(0, lineCount-height))
	end := min(lineCount, start+height)
	lines := p.cursor.CurrentLinesRange(start, end)
	syntaxOK := p.syntaxAllowed(lineCount)
	intraline := p.intralinePairs(max(0, start-intralineContextLines), min(lineCount, end+intralineContextLines))

	var rows []string
	for offset, dl := range lines {
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
		rows = append(rows, line)
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
}

func (p diffPane) renderSplit(height int) string {
	style := lipgloss.NewStyle().Width(p.width)
	lineCount := p.cursor.CurrentLineCount()
	start := clamp(p.cursor.DiffOffset(), 0, max(0, lineCount-height))
	end := min(lineCount, start+height)
	lines := p.cursor.CurrentLinesRange(start, end)
	syntaxOK := p.syntaxAllowed(lineCount)
	rows := p.splitRows(lines, start)
	if len(rows) > height {
		rows = rows[:height]
	}

	var out []string
	for _, row := range rows {
		line := p.formatSplitRow(row, syntaxOK)
		selected := row.oldIdx == p.cursor.LineIndex() || row.newIdx == p.cursor.LineIndex()
		inRange := (row.oldIdx >= 0 && p.inActiveRange(row.oldIdx)) || (row.newIdx >= 0 && p.inActiveRange(row.newIdx))
		if !selected {
			if inRange {
				line = padRightStyled(line, p.width, rangeStyle)
			} else {
				line = padRight(line, p.width)
			}
		}
		out = append(out, line)
	}
	return style.Render(strings.Join(out, "\n"))
}

func (p diffPane) intralinePairs(start, end int) map[int]string {
	lines := p.cursor.CurrentLinesRange(start, end)
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
				rows = append(rows, splitRow{new: dl, oldIdx: -1, newIdx: idx})
			} else {
				rows = append(rows, splitRow{old: dl, new: dl, oldIdx: idx, newIdx: idx})
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
			row := splitRow{oldIdx: -1, newIdx: -1}
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
	return p.syntax && lineCount <= syntaxMaxFileLines && lexers.Match(p.path) != nil
}

func (p diffPane) formatLine(dl displayLine, selected, inRange bool, rangeGlyph string, syntaxOK bool, intralineAgainst string) string {
	if dl.Line == nil {
		text := railPrefix(rangeGlyph) + dl.Text
		if selected {
			if p.wrapCursorLine {
				return wrapPadded(selectedHunkStyle.Render(text), p.width, selectedStyle)
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
	marker := " "
	for _, n := range p.fileAnnotations {
		if annotationMarker := p.workflow.MarkerFor(n, l); annotationMarker != "" {
			marker = annotationMarker
			break
		}
	}
	return p.formatUnifiedLine(l, marker, selected, inRange, rangeGlyph, syntaxOK, intralineAgainst)
}

func (p diffPane) formatSplitRow(row splitRow, syntaxOK bool) string {
	if row.hunk != "" {
		text := truncate(row.hunk, p.width)
		if row.oldIdx == p.cursor.LineIndex() || row.newIdx == p.cursor.LineIndex() {
			return padRightStyled(selectedHunkStyle.Render(text), p.width, selectedStyle)
		}
		if (row.oldIdx >= 0 && p.inActiveRange(row.oldIdx)) || (row.newIdx >= 0 && p.inActiveRange(row.newIdx)) {
			return rangeHunkStyle.Render(text)
		}
		return hunkStyle.Render(text)
	}
	selected := row.oldIdx == p.cursor.LineIndex() || row.newIdx == p.cursor.LineIndex()
	inRange := (row.oldIdx >= 0 && p.inActiveRange(row.oldIdx)) || (row.newIdx >= 0 && p.inActiveRange(row.newIdx))
	glyphIdx := row.oldIdx
	if glyphIdx < 0 {
		glyphIdx = row.newIdx
	}
	if selected {
		glyphIdx = p.cursor.LineIndex()
	}
	rangeGlyph := p.rangeGlyph(glyphIdx)
	marker := p.splitRowMarker(row)
	prefix := railCell(railGlyph(marker, rangeGlyph), selected, inRange) + gutterView(" ", selected, inRange)
	fixed := 2 + lineNoWidth + 1 + 3 + lineNoWidth + 1
	leftW := max(1, (p.width-fixed)/2)
	rightW := max(1, p.width-fixed-leftW)
	oldNo, oldKind := splitLineNoAndKind(row.old.Line, annotate.SideOld)
	newNo, newKind := splitLineNoAndKind(row.new.Line, annotate.SideNew)
	rest := p.lineNoView(oldNo, oldKind, selected, inRange) + gutterView(" ", selected, inRange) + p.splitCellText(row.old.Line, annotate.SideOld, leftW, selected, inRange, syntaxOK, row.oldAgainst) + gutterView(" │ ", selected, inRange) + p.lineNoView(newNo, newKind, selected, inRange) + gutterView(" ", selected, inRange) + p.splitCellText(row.new.Line, annotate.SideNew, rightW, selected, inRange, syntaxOK, row.newAgainst)
	line := prefix + rest
	if selected {
		line = railCell(railGlyph(marker, rangeGlyph), true, inRange) + selectedStyle.Render(" ") + rest
		return padRightStyled(truncate(line, p.width), p.width, selectedStyle)
	}
	return truncate(line, p.width)
}

func splitLineNoAndKind(line *diff.Line, side annotate.Side) (string, diff.Kind) {
	if line == nil {
		return lineNoText(0), diff.Context
	}
	if side == annotate.SideOld {
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

func (p diffPane) splitCellText(line *diff.Line, side annotate.Side, width int, selected, inRange, syntaxOK bool, intralineAgainst string) string {
	if line == nil || width <= 0 {
		return gutterView(strings.Repeat(" ", max(0, width)), selected, inRange)
	}
	kind := diff.Context
	if side == annotate.SideOld && line.Kind == diff.Delete {
		kind = diff.Delete
	}
	if side == annotate.SideNew && line.Kind == diff.Add {
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
		for _, n := range p.fileAnnotations {
			if marker := p.workflow.MarkerFor(n, *line); marker != "" {
				return marker
			}
		}
	}
	return " "
}

func (p diffPane) formatUnifiedLine(l diff.Line, marker string, selected, inRange bool, rangeGlyph string, syntaxOK bool, intralineAgainst string) string {
	oldNo := lineNoText(l.OldNo)
	newNo := lineNoText(l.NewNo)
	diffSign, body := diffMarkerBody(l.Text)
	prefix := railCell(railGlyph(marker, rangeGlyph), selected, inRange) + gutterView(" ", selected, inRange)
	bodyView := p.diffTextView(l.Kind, body, selected, inRange, syntaxOK)
	if intralineAgainst != "" && (l.Kind == diff.Add || l.Kind == diff.Delete) {
		bodyView = p.intralineTextView(l.Kind, body, intralineAgainst, selected, inRange, syntaxOK)
	}
	restPrefix := p.lineNoView(oldNo, l.Kind, selected, inRange) + gutterView(" ", selected, inRange) + p.lineNoView(newNo, l.Kind, selected, inRange) + gutterView(" ", selected, inRange) + diffSignView(diffSign, l.Kind, selected, inRange) + gutterView(" ", selected, inRange)
	if selected {
		linePrefix := railCell(railGlyph(marker, rangeGlyph), true, inRange) + selectedStyle.Render(" ") + restPrefix
		if p.wrapCursorLine {
			return p.wrapSelectedLine(linePrefix, bodyView)
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
	parts := strings.Split(xansi.Wrap(body, bodyW, " /._"), "\n")
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
	parts := strings.Split(xansi.Wrap(s, width, " /._"), "\n")
	rows := make([]string, 0, len(parts))
	for _, part := range parts {
		rows = append(rows, padRightStyled(part, width, style))
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

func (p diffPane) splitTextView(kind diff.Kind, side annotate.Side, s string, selected, inRange, syntaxOK bool) string {
	if kind == diff.Delete && side == annotate.SideOld {
		return p.syntaxBodyView(kind, s, selected, inRange, syntaxOK)
	}
	if kind == diff.Add && side == annotate.SideNew {
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
	lexer := lexers.Match(p.path)
	if lexer == nil {
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
	if !p.cursor.RangeActive() || !p.cursor.InActiveRange(idx) {
		return " "
	}
	start, end := p.cursor.RangeIndexes()
	if start == end {
		return "╭"
	}
	if idx == start {
		return "╭"
	}
	if idx == end {
		return "╰"
	}
	return "│"
}

func railGlyph(annotationMarker, rangeGlyph string) string {
	if strings.TrimSpace(rangeGlyph) != "" {
		return rangeGlyph
	}
	return annotationMarker
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
		return selectedAnnotationStyle.Render(glyph)
	}
	if inRange {
		return rangeAnnotationStyle.Render(glyph)
	}
	return annotationStyle.Render(glyph)
}

func railPrefix(glyph string) string {
	if strings.TrimSpace(glyph) == "" {
		return ""
	}
	return glyph + " "
}

func (p diffPane) inActiveRange(idx int) bool {
	return p.cursor.InActiveRange(idx)
}
