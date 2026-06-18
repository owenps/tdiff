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
	"github.com/owenps/tdiff/internal/annotations"
	"github.com/owenps/tdiff/internal/diff"
	"github.com/owenps/tdiff/internal/notes"
	"github.com/owenps/tdiff/internal/review"
)

type diffPane struct {
	path        string
	width       int
	split       bool
	syntax      bool
	syntaxCache map[string]string
	cursor      review.Cursor
	annotations annotations.Workflow
	notes       []notes.Note
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
	var fileNotes []notes.Note
	if m.store != nil {
		fileNotes = m.store.NotesFor(path)
	}
	return diffPane{
		path:        path,
		width:       width,
		split:       m.split,
		syntax:      m.syntax,
		syntaxCache: m.syntaxCache,
		cursor:      m.cursor,
		annotations: m.annotations,
		notes:       fileNotes,
	}
}

func (m Model) syntaxAllowed(lineCount int) bool {
	return m.diffPane(0).syntaxAllowed(lineCount)
}

func (p diffPane) Render(height int) string {
	style := lipgloss.NewStyle().Width(p.width)
	lineCount := p.cursor.CurrentLineCount()
	lineIdx := p.cursor.LineIndex()
	start := clamp(p.cursor.DiffOffset(), 0, max(0, lineCount-height))
	end := min(lineCount, start+height)
	lines := p.cursor.CurrentLinesRange(start, end)
	syntaxOK := p.syntaxAllowed(lineCount)

	var rows []string
	for offset, dl := range lines {
		i := start + offset
		selected := i == lineIdx
		inRange := p.inActiveRange(i)
		rangeGlyph := p.rangeGlyph(i)
		line := p.formatLine(dl, selected, inRange, rangeGlyph, syntaxOK)
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

func (p diffPane) syntaxAllowed(lineCount int) bool {
	return p.syntax && lineCount <= syntaxMaxFileLines && lexers.Match(p.path) != nil
}

func (p diffPane) formatLine(dl displayLine, selected, inRange bool, rangeGlyph string, syntaxOK bool) string {
	if dl.Line == nil {
		text := truncate(rangePrefix(rangeGlyph)+dl.Text, p.width)
		if selected {
			return padRightStyled(selectedHunkStyle.Render(text), p.width, selectedStyle)
		}
		if inRange {
			return rangeHunkStyle.Render(text)
		}
		return hunkStyle.Render(text)
	}
	l := *dl.Line
	marker := " "
	for _, n := range p.notes {
		if noteMarker := p.annotations.MarkerFor(n, l); noteMarker != "" {
			marker = noteMarker
			break
		}
	}
	if p.split {
		return p.formatSplitLine(l, marker, selected, inRange, rangeGlyph, syntaxOK)
	}
	return p.formatUnifiedLine(l, marker, selected, inRange, rangeGlyph, syntaxOK)
}

func (p diffPane) formatSplitLine(l diff.Line, marker string, selected, inRange bool, rangeGlyph string, syntaxOK bool) string {
	oldText, newText := "", ""
	oldNo := lineNoText(l.OldNo)
	newNo := lineNoText(l.NewNo)
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
	leftW := max(1, (p.width-fixed)/2)
	rightW := max(1, p.width-fixed-leftW)
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
	rest := p.lineNoView(oldNo, oldKind, selected, inRange) + gutterView(" │ ", selected, inRange) + p.splitTextView(l.Kind, notes.SideOld, oldBody, selected, inRange, syntaxOK) + gutterView(" │ ", selected, inRange) + p.lineNoView(newNo, newKind, selected, inRange) + gutterView(" ", selected, inRange) + p.splitTextView(l.Kind, notes.SideNew, newBody, selected, inRange, syntaxOK)
	if selected {
		line := rangeCell(rangeGlyph, true, inRange) + selectedStyle.Render(" ") + annotationMarker(marker, true, inRange) + rest
		return padRightStyled(truncate(line, p.width), p.width, selectedStyle)
	}
	return truncate(prefix+rest, p.width)
}

func (p diffPane) formatUnifiedLine(l diff.Line, marker string, selected, inRange bool, rangeGlyph string, syntaxOK bool) string {
	oldNo := lineNoText(l.OldNo)
	newNo := lineNoText(l.NewNo)
	diffSign, body := diffMarkerBody(l.Text)
	prefix := rangeCell(rangeGlyph, selected, inRange) + gutterView(" ", selected, inRange) + annotationMarker(marker, selected, inRange)
	rest := p.lineNoView(oldNo, l.Kind, selected, inRange) + gutterView(" ", selected, inRange) + p.lineNoView(newNo, l.Kind, selected, inRange) + gutterView(" ", selected, inRange) + diffSignView(diffSign, l.Kind, selected, inRange) + gutterView(" ", selected, inRange) + p.diffTextView(l.Kind, body, selected, inRange, syntaxOK)
	if selected {
		line := rangeCell(rangeGlyph, true, inRange) + selectedStyle.Render(" ") + annotationMarker(marker, true, inRange) + rest
		return padRightStyled(truncate(line, p.width), p.width, selectedStyle)
	}
	return truncate(prefix+rest, p.width)
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
	return p.syntaxView(s)
}

func (p diffPane) splitTextView(kind diff.Kind, side notes.Side, s string, selected, inRange, syntaxOK bool) string {
	if kind == diff.Delete && side == notes.SideOld {
		return p.syntaxBodyView(kind, s, selected, inRange, syntaxOK)
	}
	if kind == diff.Add && side == notes.SideNew {
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
	return p.syntaxView(s)
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

func (p diffPane) inActiveRange(idx int) bool {
	return p.cursor.InActiveRange(idx)
}
