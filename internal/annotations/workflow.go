package annotations

import (
	"fmt"
	"strings"

	"github.com/owenps/tdiff/internal/annotate"
	"github.com/owenps/tdiff/internal/diff"
	"github.com/owenps/tdiff/internal/review"
)

type Store interface {
	Add(annotate.Annotation) error
	UpdateBody(id, body string) error
	Delete(id string) error
	AnnotationsFor(path string) []annotate.Annotation
}

type Workflow struct {
	store Store
}

type Target struct {
	Side       annotate.Side
	LineStart  int
	LineEnd    int
	HunkHeader string
	Context    string
}

type DiffLine struct {
	Line       diff.Line
	HunkHeader string
}

func NewWorkflow(store Store) Workflow {
	return Workflow{store: store}
}

func (w Workflow) TargetForLine(dl DiffLine) (Target, error) {
	side, line, ok := lineTarget(dl.Line)
	if !ok {
		return Target{}, fmt.Errorf("no line selected")
	}
	return Target{Side: side, LineStart: line, LineEnd: line, HunkHeader: dl.HunkHeader, Context: dl.Line.Text}, nil
}

func (w Workflow) TargetForDisplayLine(dl review.DisplayLine) (Target, error) {
	if dl.Line == nil {
		return Target{}, fmt.Errorf("no line selected")
	}
	return w.TargetForLine(DiffLine{Line: *dl.Line, HunkHeader: dl.HunkHeader})
}

func (w Workflow) TargetForDisplayRange(lines []review.DisplayLine) (Target, error) {
	selected := make([]DiffLine, 0, len(lines))
	for _, dl := range lines {
		if dl.Line == nil {
			continue
		}
		selected = append(selected, DiffLine{Line: *dl.Line, HunkHeader: dl.HunkHeader})
	}
	return w.TargetForRange(selected)
}

func (w Workflow) TargetForRange(lines []DiffLine) (Target, error) {
	var target Target
	var context []string
	for _, dl := range lines {
		side, line, ok := lineTarget(dl.Line)
		if !ok {
			continue
		}
		if target.LineStart == 0 {
			target.Side = side
			target.LineStart = line
			target.LineEnd = line
			target.HunkHeader = dl.HunkHeader
		} else if target.Side != side {
			return Target{}, fmt.Errorf("mixed old/new range unsupported")
		} else {
			target.LineStart = min(target.LineStart, line)
			target.LineEnd = max(target.LineEnd, line)
		}
		context = append(context, dl.Line.Text)
	}
	if target.LineStart == 0 {
		return Target{}, fmt.Errorf("range has no diff lines")
	}
	target.Context = strings.Join(context, "\n")
	return target, nil
}

func (w Workflow) AnnotationAt(path string, line diff.Line) (annotate.Annotation, bool) {
	side, lineNo, ok := lineTarget(line)
	if !ok {
		return annotate.Annotation{}, false
	}
	for _, n := range w.store.AnnotationsFor(path) {
		if n.Side == side && n.LineStart <= lineNo && lineNo <= n.LineEnd {
			return n, true
		}
	}
	return annotate.Annotation{}, false
}

func (w Workflow) MarkerFor(annotation annotate.Annotation, line diff.Line) string {
	lineNo := 0
	if annotation.Side == annotate.SideNew {
		lineNo = line.NewNo
	} else {
		lineNo = line.OldNo
	}
	if lineNo == 0 || lineNo < annotation.LineStart || lineNo > annotation.LineEnd {
		return ""
	}
	if annotation.LineStart == annotation.LineEnd || lineNo == annotation.LineStart {
		return "●"
	}
	if lineNo == annotation.LineEnd {
		return "╰"
	}
	return "│"
}

func (w Workflow) Save(path, diffHash, editingID string, target Target, body string) error {
	body = strings.TrimSpace(body)
	if body == "" {
		return fmt.Errorf("empty annotation")
	}
	if editingID != "" {
		return w.store.UpdateBody(editingID, body)
	}
	if target.LineStart == 0 {
		return fmt.Errorf("no annotation target")
	}
	if w.overlapsExisting(path, target) {
		return fmt.Errorf("annotation overlaps existing annotation")
	}
	return w.store.Add(annotate.Annotation{
		Path:       path,
		Side:       target.Side,
		Line:       target.LineStart,
		LineStart:  target.LineStart,
		LineEnd:    target.LineEnd,
		HunkHeader: target.HunkHeader,
		Context:    target.Context,
		Body:       body,
		DiffHash:   diffHash,
	})
}

func (w Workflow) Delete(id string) error {
	return w.store.Delete(id)
}

func (w Workflow) overlapsExisting(path string, target Target) bool {
	start := target.LineStart
	end := target.LineEnd
	if end == 0 {
		end = start
	}
	for _, n := range w.store.AnnotationsFor(path) {
		if n.Side != target.Side {
			continue
		}
		annotationStart, annotationEnd := annotationRange(n)
		if start <= annotationEnd && annotationStart <= end {
			return true
		}
	}
	return false
}

func annotationRange(n annotate.Annotation) (int, int) {
	start := n.LineStart
	if start == 0 {
		start = n.Line
	}
	end := n.LineEnd
	if end == 0 {
		end = start
	}
	return start, end
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
