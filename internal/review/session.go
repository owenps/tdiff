package review

import (
	"fmt"

	"github.com/owenps/tdiff/internal/annotate"
	"github.com/owenps/tdiff/internal/diff"
)

type ViewedStore interface {
	MarkViewed(path, diffHash string) error
	ClearViewed(path string) error
	IsViewed(path, diffHash string) bool
}

type AnnotationStore interface {
	AnnotationsFor(path string) []annotate.Annotation
}

type ViewedToggleResult struct {
	Path     string
	Viewed   bool
	Advanced bool
}

type RangeToggleResult struct {
	Started   bool
	Cancelled bool
}

type LineWindow struct {
	Path        string
	Lines       []DisplayLine
	Start       int
	End         int
	LineCount   int
	LineIndex   int
	RangeActive bool
	RangeStart  int
	RangeEnd    int
}

func (w LineWindow) InActiveRange(idx int) bool {
	return w.RangeActive && w.RangeStart <= idx && idx <= w.RangeEnd
}

func (w LineWindow) RangeGlyph(idx int) string {
	if !w.InActiveRange(idx) {
		return " "
	}
	if w.RangeStart == w.RangeEnd || idx == w.RangeStart {
		return "╭"
	}
	if idx == w.RangeEnd {
		return "╰"
	}
	return "│"
}

type Session struct {
	allFiles []diff.File
	diffHash string
	cursor   Cursor

	hideViewed      bool
	annotationsOnly bool
	viewed          ViewedStore
	annotations     AnnotationStore
}

func NewSession(files []diff.File) Session {
	return Session{allFiles: files, cursor: NewCursor(files)}
}

func (s *Session) SetSnapshot(files []diff.File, diffHash string) {
	s.allFiles = files
	s.diffHash = diffHash
	s.applyFilters()
}

func (s *Session) SetStores(viewed ViewedStore, annotations AnnotationStore) {
	s.viewed = viewed
	s.annotations = annotations
	s.applyFilters()
}

func (s *Session) SetFilters(hideViewed, annotationsOnly bool) {
	s.hideViewed = hideViewed
	s.annotationsOnly = annotationsOnly
	s.applyFilters()
}

func (s *Session) ToggleHideViewed() bool {
	s.hideViewed = !s.hideViewed
	s.applyFilters()
	return s.hideViewed
}

func (s *Session) ToggleAnnotationsOnly() bool {
	s.annotationsOnly = !s.annotationsOnly
	s.applyFilters()
	return s.annotationsOnly
}

func (s Session) HideViewed() bool { return s.hideViewed }

func (s Session) AnnotationsOnly() bool { return s.annotationsOnly }

func (s *Session) RefreshFilters() {
	s.applyFilters()
}

func (s *Session) applyFilters() {
	var isViewed func(path, diffHash string) bool
	if s.viewed != nil {
		isViewed = s.viewed.IsViewed
	}
	var annotationCount func(path string) int
	if s.annotations != nil {
		annotationCount = s.AnnotationCount
	}
	s.cursor.SetFilteredFiles(s.allFiles, FileFilter{
		HideViewed:      s.hideViewed,
		AnnotationsOnly: s.annotationsOnly,
		DiffHash:        s.diffHash,
		IsViewed:        isViewed,
		AnnotationCount: annotationCount,
	})
}

func (s Session) AllFiles() []diff.File { return s.allFiles }
func (s Session) DiffHash() string      { return s.diffHash }

func (s Session) Files() []diff.File { return s.cursor.Files() }
func (s Session) FileIndex() int     { return s.cursor.FileIndex() }
func (s Session) LineIndex() int     { return s.cursor.LineIndex() }
func (s Session) DiffOffset() int    { return s.cursor.DiffOffset() }
func (s Session) RangeActive() bool  { return s.cursor.RangeActive() }

func (s Session) CurrentLines() []DisplayLine { return s.cursor.CurrentLines() }
func (s Session) CurrentLineCount() int       { return s.cursor.CurrentLineCount() }
func (s Session) CurrentLinesRange(start, end int) []DisplayLine {
	return s.cursor.CurrentLinesRange(start, end)
}
func (s Session) DisplayLineAt(target int) DisplayLine { return s.cursor.DisplayLineAt(target) }
func (s Session) SelectedLine() DisplayLine            { return s.cursor.SelectedLine() }
func (s Session) CurrentPath() string                  { return s.cursor.CurrentPath() }
func (s Session) RangeIndexes() (int, int)             { return s.cursor.RangeIndexes() }
func (s Session) RangeLines() []DisplayLine            { return s.cursor.RangeLines() }

func (s Session) LineWindow(height int) LineWindow {
	lineCount := s.CurrentLineCount()
	start := clamp(s.DiffOffset(), 0, max(0, lineCount-height))
	end := min(lineCount, start+height)
	return s.LineWindowRange(start, end)
}

func (s Session) LineWindowRange(start, end int) LineWindow {
	lineCount := s.CurrentLineCount()
	start = clamp(start, 0, lineCount)
	end = clamp(end, start, lineCount)
	rangeStart, rangeEnd := 0, 0
	if s.RangeActive() {
		rangeStart, rangeEnd = s.RangeIndexes()
	}
	return LineWindow{
		Path:        s.CurrentPath(),
		Lines:       s.CurrentLinesRange(start, end),
		Start:       start,
		End:         end,
		LineCount:   lineCount,
		LineIndex:   s.LineIndex(),
		RangeActive: s.RangeActive(),
		RangeStart:  rangeStart,
		RangeEnd:    rangeEnd,
	}
}

func (s *Session) MoveLine(delta, height int) { s.cursor.MoveLine(delta, height) }
func (s *Session) MoveFile(delta int)         { s.cursor.MoveFile(delta) }
func (s *Session) JumpTop()                   { s.cursor.JumpTop() }
func (s *Session) JumpBottom(height int)      { s.cursor.JumpBottom(height) }
func (s *Session) NextHunk(height int) bool   { return s.cursor.NextHunk(height) }
func (s *Session) PrevHunk(height int) bool   { return s.cursor.PrevHunk(height) }
func (s *Session) JumpToFileLine(line, height int) bool {
	return s.cursor.JumpToFileLine(line, height)
}
func (s *Session) JumpToIndex(fileIdx, lineIdx, height int) bool {
	return s.cursor.JumpToIndex(fileIdx, lineIdx, height)
}
func (s *Session) EnsureVisible(height int) { s.cursor.EnsureVisible(height) }
func (s *Session) StartRange() bool         { return s.cursor.StartRange() }
func (s *Session) CancelRange()             { s.cursor.CancelRange() }
func (s Session) InActiveRange(idx int) bool {
	return s.cursor.InActiveRange(idx)
}
func (s Session) RangeGlyph(idx int) string {
	rangeStart, rangeEnd := 0, 0
	if s.RangeActive() {
		rangeStart, rangeEnd = s.RangeIndexes()
	}
	return LineWindow{RangeActive: s.RangeActive(), RangeStart: rangeStart, RangeEnd: rangeEnd}.RangeGlyph(idx)
}
func (s *Session) ToggleRange() RangeToggleResult {
	if s.RangeActive() {
		s.CancelRange()
		return RangeToggleResult{Cancelled: true}
	}
	if s.StartRange() {
		return RangeToggleResult{Started: true}
	}
	return RangeToggleResult{}
}
func (s *Session) AdvanceToNextFile(matches func(diff.File) bool) bool {
	return s.cursor.AdvanceToNextFile(matches)
}
func (s Session) IsViewed(path string) bool {
	return s.viewed != nil && s.viewed.IsViewed(path, s.diffHash)
}

func (s Session) AnnotationCount(path string) int {
	if s.annotations == nil {
		return 0
	}
	return len(s.annotations.AnnotationsFor(path))
}

func (s *Session) ToggleViewed() (ViewedToggleResult, error) {
	path := s.CurrentPath()
	if path == "" {
		return ViewedToggleResult{}, nil
	}
	if s.viewed == nil {
		return ViewedToggleResult{}, fmt.Errorf("viewed store not configured")
	}
	if s.viewed.IsViewed(path, s.diffHash) {
		if err := s.viewed.ClearViewed(path); err != nil {
			return ViewedToggleResult{}, err
		}
		s.applyFilters()
		return ViewedToggleResult{Path: path}, nil
	}
	if err := s.viewed.MarkViewed(path, s.diffHash); err != nil {
		return ViewedToggleResult{}, err
	}
	result := ViewedToggleResult{Path: path, Viewed: true}
	if s.hideViewed {
		s.applyFilters()
		return result, nil
	}
	result.Advanced = s.AdvanceToNextUnviewed()
	return result, nil
}

func (s *Session) AdvanceToNextUnviewed() bool {
	return s.cursor.AdvanceToNextUnviewed(s.diffHash, func(path, diffHash string) bool {
		return s.viewed != nil && s.viewed.IsViewed(path, diffHash)
	})
}
func (s Session) AnnotationPositions() []AnnotationPosition {
	if s.annotations == nil {
		return nil
	}
	return s.cursor.AnnotationPositions(s.annotations.AnnotationsFor)
}
func (s *Session) JumpAnnotation(delta, height int) (int, int, bool) {
	if s.annotations == nil {
		return 0, 0, false
	}
	return s.cursor.JumpAnnotation(delta, height, s.annotations.AnnotationsFor)
}
func (s Session) SelectedAnnotation() (annotate.Annotation, bool) {
	if s.annotations == nil {
		return annotate.Annotation{}, false
	}
	dl := s.SelectedLine()
	if dl.Line == nil {
		return annotate.Annotation{}, false
	}
	return AnnotationAtLine(s.annotations.AnnotationsFor(s.CurrentPath()), *dl.Line)
}

func AnnotationAtLine(annotations []annotate.Annotation, line diff.Line) (annotate.Annotation, bool) {
	side, lineNo, ok := lineTarget(line)
	if !ok {
		return annotate.Annotation{}, false
	}
	for _, n := range annotations {
		if n.Side == side && n.LineStart <= lineNo && lineNo <= n.LineEnd {
			return n, true
		}
	}
	return annotate.Annotation{}, false
}

func lineTarget(l diff.Line) (annotate.Side, int, bool) {
	if l.Kind == diff.Delete {
		return annotate.SideOld, l.OldNo, l.OldNo > 0
	}
	if l.NewNo > 0 {
		return annotate.SideNew, l.NewNo, true
	}
	if l.OldNo > 0 {
		return annotate.SideOld, l.OldNo, true
	}
	return annotate.SideNew, 0, false
}
