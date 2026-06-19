package review

import (
	"github.com/owenps/tdiff/internal/annotate"
	"github.com/owenps/tdiff/internal/diff"
)

type DisplayLine struct {
	Line       *diff.Line
	Text       string
	HunkHeader string
}

type Cursor struct {
	files         []diff.File
	displayLines  [][]DisplayLine
	fileIdx       int
	lineIdx       int
	diffOffset    int
	rangeActive   bool
	rangeStartIdx int
}

func NewCursor(files []diff.File) Cursor {
	c := Cursor{}
	c.SetFiles(files)
	return c
}

func (c *Cursor) SetFiles(files []diff.File) {
	c.files = files
	c.displayLines = displayLinesForFiles(c.files)
	if c.fileIdx >= len(c.files) {
		c.fileIdx = max(0, len(c.files)-1)
	}
	c.lineIdx = 0
	c.diffOffset = 0
	c.rangeActive = false
}

type FileFilter struct {
	HideViewed      bool
	AnnotationsOnly bool
	DiffHash        string
	IsViewed        func(path, diffHash string) bool
	AnnotationCount func(path string) int
}

func (c *Cursor) SetFilteredFiles(files []diff.File, filter FileFilter) {
	c.SetFiles(FilterFiles(files, filter))
}

func FilterFiles(files []diff.File, filter FileFilter) []diff.File {
	filtered := make([]diff.File, 0, len(files))
	for _, f := range files {
		path := f.Path()
		if filter.HideViewed && filter.IsViewed != nil && filter.IsViewed(path, filter.DiffHash) {
			continue
		}
		if filter.AnnotationsOnly && filter.AnnotationCount != nil && filter.AnnotationCount(path) == 0 {
			continue
		}
		filtered = append(filtered, f)
	}
	return filtered
}

func (c Cursor) Files() []diff.File { return c.files }
func (c Cursor) FileIndex() int     { return c.fileIdx }
func (c Cursor) LineIndex() int     { return c.lineIdx }
func (c Cursor) DiffOffset() int    { return c.diffOffset }
func (c Cursor) RangeActive() bool  { return c.rangeActive }

func (c Cursor) CurrentLines() []DisplayLine {
	return c.currentDisplayLines()
}

func (c Cursor) CurrentLineCount() int {
	return len(c.currentDisplayLines())
}

func (c Cursor) CurrentLinesRange(start, end int) []DisplayLine {
	lines := c.currentDisplayLines()
	if len(lines) == 0 || start >= end {
		return nil
	}
	start = clamp(start, 0, len(lines))
	end = clamp(end, start, len(lines))
	return lines[start:end]
}

func (c Cursor) DisplayLineAt(target int) DisplayLine {
	lines := c.currentDisplayLines()
	if target < 0 || target >= len(lines) {
		return DisplayLine{}
	}
	return lines[target]
}

func (c Cursor) SelectedLine() DisplayLine {
	return c.DisplayLineAt(c.lineIdx)
}

func (c Cursor) CurrentPath() string {
	if len(c.files) == 0 || c.fileIdx >= len(c.files) {
		return ""
	}
	return c.files[c.fileIdx].Path()
}

func (c *Cursor) MoveLine(delta, height int) {
	c.lineIdx = clamp(c.lineIdx+delta, 0, max(0, c.CurrentLineCount()-1))
	c.EnsureVisible(height)
}

func (c *Cursor) MoveFile(delta int) {
	c.fileIdx = clamp(c.fileIdx+delta, 0, max(0, len(c.files)-1))
	c.lineIdx = 0
	c.diffOffset = 0
	c.rangeActive = false
}

func (c *Cursor) JumpTop() {
	c.lineIdx = 0
	c.diffOffset = 0
}

func (c *Cursor) JumpBottom(height int) {
	c.lineIdx = max(0, c.CurrentLineCount()-1)
	c.EnsureVisible(height)
}

func (c *Cursor) NextHunk(height int) bool {
	for i, dl := range c.CurrentLines() {
		if i > c.lineIdx && dl.Line == nil {
			c.lineIdx = i
			c.EnsureVisible(height)
			return true
		}
	}
	return false
}

func (c *Cursor) PrevHunk(height int) bool {
	lines := c.CurrentLines()
	for i := min(c.lineIdx-1, len(lines)-1); i >= 0; i-- {
		if lines[i].Line == nil {
			c.lineIdx = i
			c.EnsureVisible(height)
			return true
		}
	}
	return false
}

func (c *Cursor) JumpToFileLine(line, height int) bool {
	for i, dl := range c.CurrentLines() {
		if dl.Line != nil && (dl.Line.OldNo == line || dl.Line.NewNo == line) {
			c.lineIdx = i
			c.EnsureVisible(height)
			return true
		}
	}
	return false
}

func (c *Cursor) JumpToIndex(fileIdx, lineIdx, height int) bool {
	if fileIdx < 0 || fileIdx >= len(c.files) {
		return false
	}
	c.fileIdx = fileIdx
	c.lineIdx = clamp(lineIdx, 0, max(0, c.CurrentLineCount()-1))
	c.diffOffset = 0
	c.rangeActive = false
	c.EnsureVisible(height)
	return true
}

func (c *Cursor) EnsureVisible(height int) {
	lines := c.CurrentLineCount()
	if lines == 0 {
		c.diffOffset = 0
		return
	}
	if c.lineIdx < c.diffOffset {
		c.diffOffset = c.lineIdx
	}
	if c.lineIdx >= c.diffOffset+height {
		c.diffOffset = c.lineIdx - height + 1
	}
	c.diffOffset = clamp(c.diffOffset, 0, max(0, lines-height))
}

func (c *Cursor) StartRange() bool {
	if c.SelectedLine().Line == nil {
		return false
	}
	c.rangeActive = true
	c.rangeStartIdx = c.lineIdx
	return true
}

func (c *Cursor) CancelRange() {
	c.rangeActive = false
}

func (c Cursor) InActiveRange(idx int) bool {
	if !c.rangeActive {
		return false
	}
	start, end := c.RangeIndexes()
	return start <= idx && idx <= end
}

func (c Cursor) RangeIndexes() (int, int) {
	start, end := c.rangeStartIdx, c.lineIdx
	if start > end {
		start, end = end, start
	}
	return start, end
}

func (c Cursor) RangeLines() []DisplayLine {
	start, end := c.RangeIndexes()
	return c.CurrentLinesRange(start, min(end+1, c.CurrentLineCount()))
}

func (c *Cursor) AdvanceToNextFile(matches func(diff.File) bool) bool {
	for step := 1; step < len(c.files); step++ {
		i := (c.fileIdx + step) % len(c.files)
		if matches(c.files[i]) {
			c.fileIdx = i
			c.lineIdx = 0
			c.diffOffset = 0
			return true
		}
	}
	return false
}

func (c *Cursor) AdvanceToNextUnviewed(diffHash string, isViewed func(path, diffHash string) bool) bool {
	return c.AdvanceToNextFile(func(f diff.File) bool {
		return isViewed == nil || !isViewed(f.Path(), diffHash)
	})
}

type AnnotationPosition struct {
	FileIdx    int
	LineIdx    int
	Annotation annotate.Annotation
}

func (c Cursor) AnnotationPositions(annotationsForPath func(string) []annotate.Annotation) []AnnotationPosition {
	var out []AnnotationPosition
	for fileIdx, f := range c.files {
		path := f.Path()
		lines := c.displayLinesForFileIndex(fileIdx)
		for _, annotation := range annotationsForPath(path) {
			for lineIdx, dl := range lines {
				if dl.Line != nil && AnnotationMatchesLine(annotation, *dl.Line) {
					out = append(out, AnnotationPosition{FileIdx: fileIdx, LineIdx: lineIdx, Annotation: annotation})
					break
				}
			}
		}
	}
	return out
}

func (c *Cursor) JumpAnnotation(delta, height int, annotationsForPath func(string) []annotate.Annotation) (int, int, bool) {
	positions := c.AnnotationPositions(annotationsForPath)
	if len(positions) == 0 {
		return 0, 0, false
	}
	idx := -1
	if delta > 0 {
		idx = 0
		for i, p := range positions {
			if p.FileIdx > c.fileIdx || (p.FileIdx == c.fileIdx && p.LineIdx > c.lineIdx) {
				idx = i
				break
			}
		}
	} else {
		idx = len(positions) - 1
		for i := len(positions) - 1; i >= 0; i-- {
			p := positions[i]
			if p.FileIdx < c.fileIdx || (p.FileIdx == c.fileIdx && p.LineIdx < c.lineIdx) {
				idx = i
				break
			}
		}
	}
	p := positions[idx]
	if !c.JumpToIndex(p.FileIdx, p.LineIdx, height) {
		return 0, len(positions), false
	}
	return idx + 1, len(positions), true
}

func (c Cursor) SelectedAnnotation(annotationAt func(path string, line diff.Line) (annotate.Annotation, bool)) (annotate.Annotation, bool) {
	dl := c.SelectedLine()
	if dl.Line == nil || annotationAt == nil {
		return annotate.Annotation{}, false
	}
	return annotationAt(c.CurrentPath(), *dl.Line)
}

func DisplayLinesForFile(f diff.File) []DisplayLine {
	return displayLinesForFile(&f)
}

func (c Cursor) currentDisplayLines() []DisplayLine {
	if len(c.displayLines) == 0 || c.fileIdx < 0 || c.fileIdx >= len(c.displayLines) {
		return nil
	}
	return c.displayLines[c.fileIdx]
}

func (c Cursor) displayLinesForFileIndex(fileIdx int) []DisplayLine {
	if fileIdx < 0 || fileIdx >= len(c.displayLines) {
		return nil
	}
	return c.displayLines[fileIdx]
}

func displayLinesForFiles(files []diff.File) [][]DisplayLine {
	out := make([][]DisplayLine, len(files))
	for i := range files {
		out[i] = displayLinesForFile(&files[i])
	}
	return out
}

func displayLinesForFile(f *diff.File) []DisplayLine {
	count := 0
	for _, h := range f.Hunks {
		count += 1 + len(h.Lines)
	}
	out := make([]DisplayLine, 0, count)
	for hunkIdx := range f.Hunks {
		h := &f.Hunks[hunkIdx]
		header := h.Header
		out = append(out, DisplayLine{Text: header, HunkHeader: header})
		for i := range h.Lines {
			line := &h.Lines[i]
			out = append(out, DisplayLine{Line: line, Text: line.Text, HunkHeader: header})
		}
	}
	return out
}

func AnnotationMatchesLine(annotation annotate.Annotation, line diff.Line) bool {
	lineNo := line.NewNo
	if annotation.Side == annotate.SideOld {
		lineNo = line.OldNo
	}
	return lineNo >= annotation.LineStart && lineNo <= annotation.LineEnd
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
