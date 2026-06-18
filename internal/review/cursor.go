package review

import "github.com/owenps/tdiff/internal/diff"

type DisplayLine struct {
	Line       *diff.Line
	Text       string
	HunkHeader string
}

type Cursor struct {
	files         []diff.File
	fileIdx       int
	lineIdx       int
	diffOffset    int
	rangeActive   bool
	rangeStartIdx int
}

func NewCursor(files []diff.File) Cursor {
	return Cursor{files: files}
}

func (c *Cursor) SetFiles(files []diff.File) {
	c.files = files
	if c.fileIdx >= len(c.files) {
		c.fileIdx = max(0, len(c.files)-1)
	}
	c.lineIdx = 0
	c.diffOffset = 0
	c.rangeActive = false
}

func (c Cursor) Files() []diff.File { return c.files }
func (c Cursor) FileIndex() int     { return c.fileIdx }
func (c Cursor) LineIndex() int     { return c.lineIdx }
func (c Cursor) DiffOffset() int    { return c.diffOffset }
func (c Cursor) RangeActive() bool  { return c.rangeActive }

func (c Cursor) CurrentLines() []DisplayLine {
	return c.CurrentLinesRange(0, c.CurrentLineCount())
}

func (c Cursor) CurrentLineCount() int {
	if len(c.files) == 0 || c.fileIdx >= len(c.files) {
		return 0
	}
	count := 0
	for _, h := range c.files[c.fileIdx].Hunks {
		count += 1 + len(h.Lines)
	}
	return count
}

func (c Cursor) CurrentLinesRange(start, end int) []DisplayLine {
	if len(c.files) == 0 || c.fileIdx >= len(c.files) || start >= end {
		return nil
	}
	if start < 0 {
		start = 0
	}
	idx := 0
	out := make([]DisplayLine, 0, end-start)
	for _, h := range c.files[c.fileIdx].Hunks {
		header := h.Header
		if start <= idx && idx < end {
			out = append(out, DisplayLine{Text: header, HunkHeader: header})
		}
		idx++
		for i := range h.Lines {
			if idx >= end {
				return out
			}
			if idx >= start {
				line := h.Lines[i]
				out = append(out, DisplayLine{Line: &line, Text: line.Text, HunkHeader: header})
			}
			idx++
		}
	}
	return out
}

func (c Cursor) DisplayLineAt(target int) DisplayLine {
	lines := c.CurrentLinesRange(target, target+1)
	if len(lines) == 0 {
		return DisplayLine{}
	}
	return lines[0]
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
