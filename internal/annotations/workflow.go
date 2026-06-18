package annotations

import (
	"fmt"
	"strings"

	"github.com/owenps/tdiff/internal/diff"
	"github.com/owenps/tdiff/internal/notes"
)

type Store interface {
	Add(notes.Note) error
	UpdateBody(id, body string) error
	Delete(id string) error
	NotesFor(path string) []notes.Note
}

type Workflow struct {
	store Store
}

type Target struct {
	Side       notes.Side
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

func (w Workflow) AnnotationAt(path string, line diff.Line) (notes.Note, bool) {
	side, lineNo, ok := lineTarget(line)
	if !ok {
		return notes.Note{}, false
	}
	for _, n := range w.store.NotesFor(path) {
		if n.Side == side && n.LineStart <= lineNo && lineNo <= n.LineEnd {
			return n, true
		}
	}
	return notes.Note{}, false
}

func (w Workflow) MarkerFor(note notes.Note, line diff.Line) string {
	lineNo := 0
	if note.Side == notes.SideNew {
		lineNo = line.NewNo
	} else {
		lineNo = line.OldNo
	}
	if lineNo == 0 || lineNo < note.LineStart || lineNo > note.LineEnd {
		return ""
	}
	if note.LineStart == note.LineEnd || lineNo == note.LineStart {
		return "●"
	}
	if lineNo == note.LineEnd {
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
	return w.store.Add(notes.Note{
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

func lineTarget(l diff.Line) (notes.Side, int, bool) {
	if l.Kind == diff.Delete {
		return notes.SideOld, l.OldNo, l.OldNo > 0
	}
	if l.NewNo > 0 {
		return notes.SideNew, l.NewNo, true
	}
	if l.OldNo > 0 {
		return notes.SideOld, l.OldNo, true
	}
	return notes.SideNew, 0, false
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
