package threadtarget

import (
	"github.com/owenps/tdiff/internal/diff"
	"github.com/owenps/tdiff/internal/thread"
)

func ForLine(l diff.Line) (thread.Side, int, bool) {
	if l.Kind == diff.Delete {
		return thread.SideOld, l.OldNo, l.OldNo > 0
	}
	if l.NewNo > 0 {
		return thread.SideNew, l.NewNo, true
	}
	if l.OldNo > 0 {
		return thread.SideOld, l.OldNo, true
	}
	return thread.SideNew, 0, false
}

func Range(n thread.Thread) (int, int) {
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

func MatchesLine(n thread.Thread, l diff.Line) bool {
	lineNo := l.NewNo
	if n.Side == thread.SideOld {
		lineNo = l.OldNo
	}
	start, end := Range(n)
	return lineNo >= start && lineNo <= end
}
