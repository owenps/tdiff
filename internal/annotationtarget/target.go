package annotationtarget

import (
	"github.com/owenps/tdiff/internal/annotate"
	"github.com/owenps/tdiff/internal/diff"
)

func ForLine(l diff.Line) (annotate.Side, int, bool) {
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

func Range(n annotate.Annotation) (int, int) {
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

func MatchesLine(n annotate.Annotation, l diff.Line) bool {
	lineNo := l.NewNo
	if n.Side == annotate.SideOld {
		lineNo = l.OldNo
	}
	start, end := Range(n)
	return lineNo >= start && lineNo <= end
}
