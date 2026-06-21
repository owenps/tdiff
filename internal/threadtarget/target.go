package threadtarget

import (
	"strings"

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

func LineNumber(l diff.Line, side thread.Side) int {
	if side == thread.SideOld {
		return l.OldNo
	}
	return l.NewNo
}

func MatchesLine(n thread.Thread, l diff.Line) bool {
	lineNo := LineNumber(l, n.Side)
	start, end := Range(n)
	return lineNo > 0 && lineNo >= start && lineNo <= end
}

func CurrentInFiles(n thread.Thread, files []diff.File) bool {
	for _, f := range files {
		if f.Path() != n.Path {
			continue
		}
		for _, h := range f.Hunks {
			for _, l := range h.Lines {
				if MatchesLine(n, l) {
					return true
				}
			}
		}
	}
	return false
}

func ContextForRange(files []diff.File, path string, side thread.Side, start, end int) (string, string, bool) {
	if end == 0 {
		end = start
	}
	for _, f := range files {
		if f.Path() != path {
			continue
		}
		for _, h := range f.Hunks {
			var lines []string
			for _, l := range h.Lines {
				lineNo := LineNumber(l, side)
				if lineNo >= start && lineNo <= end {
					lines = append(lines, l.Text)
				}
			}
			if len(lines) > 0 {
				return h.Header, strings.Join(lines, "\n"), true
			}
		}
	}
	return "", "", false
}
