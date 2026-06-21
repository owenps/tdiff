package threadworkflow

import (
	"fmt"
	"strings"

	"github.com/owenps/tdiff/internal/diff"
	"github.com/owenps/tdiff/internal/review"
	"github.com/owenps/tdiff/internal/thread"
	"github.com/owenps/tdiff/internal/threadtarget"
)

type Store interface {
	Add(thread.Thread) error
	UpdateFirstMessage(id, body string) error
	Delete(id string) error
	ThreadsFor(path string) []thread.Thread
}

type Workflow struct {
	store Store
}

type Target struct {
	Side       thread.Side
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
	side, line, ok := threadtarget.ForLine(dl.Line)
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
		side, line, ok := threadtarget.ForLine(dl.Line)
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

func (w Workflow) ThreadAt(path string, line diff.Line) (thread.Thread, bool) {
	side, _, ok := threadtarget.ForLine(line)
	if !ok {
		return thread.Thread{}, false
	}
	for _, n := range w.store.ThreadsFor(path) {
		if n.Side == side && threadtarget.MatchesLine(n, line) {
			return n, true
		}
	}
	return thread.Thread{}, false
}

func (w Workflow) MarkerFor(t thread.Thread, line diff.Line) string {
	lineNo := 0
	if t.Side == thread.SideNew {
		lineNo = line.NewNo
	} else {
		lineNo = line.OldNo
	}
	start, end := threadtarget.Range(t)
	if lineNo == 0 || lineNo < start || lineNo > end {
		return ""
	}
	if start == end || lineNo == start {
		return "●"
	}
	if lineNo == end {
		return "╰"
	}
	return "│"
}

func (w Workflow) Save(path, diffHash, editingID string, target Target, body string) error {
	body = strings.TrimSpace(body)
	if body == "" {
		return fmt.Errorf("empty thread")
	}
	if editingID != "" {
		return w.store.UpdateFirstMessage(editingID, body)
	}
	if target.LineStart == 0 {
		return fmt.Errorf("no thread target")
	}
	if w.overlapsExisting(path, target) {
		return fmt.Errorf("thread overlaps existing thread")
	}
	return w.store.Add(thread.Thread{
		Path:       path,
		Side:       target.Side,
		Line:       target.LineStart,
		LineStart:  target.LineStart,
		LineEnd:    target.LineEnd,
		HunkHeader: target.HunkHeader,
		Context:    target.Context,
		Messages:   []thread.Message{{Actor: thread.ActorHuman, Body: body}},
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
	for _, n := range w.store.ThreadsFor(path) {
		if n.Side != target.Side {
			continue
		}
		threadStart, threadEnd := threadtarget.Range(n)
		if start <= threadEnd && threadStart <= end {
			return true
		}
	}
	return false
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
