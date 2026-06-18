package app

import (
	"testing"

	"github.com/owenps/tdiff/internal/diff"
	"github.com/owenps/tdiff/internal/notes"
	"github.com/owenps/tdiff/internal/review"
)

func BenchmarkRenderDiffLargeNoSyntax(b *testing.B) {
	m := benchModel(100_000)
	m.syntax = false
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = m.renderDiff(40)
	}
}

func BenchmarkRenderDiffLargeSyntaxSkipped(b *testing.B) {
	m := benchModel(100_000)
	m.syntax = true
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = m.renderDiff(40)
	}
}

func benchModel(lines int) Model {
	diffLines := make([]diff.Line, lines)
	for i := range diffLines {
		diffLines[i] = diff.Line{Kind: diff.Add, NewNo: i + 1, Text: "+func example() { return }"}
	}
	file := diff.File{NewPath: "big.go", Hunks: []diff.Hunk{{Header: "@@ -0,0 +1 @@", Lines: diffLines}}}
	return Model{
		store:       &notes.Store{},
		cursor:      review.NewCursor([]diff.File{file}),
		width:       120,
		height:      40,
		syntaxCache: make(map[string]string),
	}
}
