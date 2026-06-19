package app

import (
	"fmt"
	"testing"

	"github.com/owenps/tdiff/internal/annotate"
	"github.com/owenps/tdiff/internal/diff"
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

func BenchmarkRenderSplitLargeSyntaxSkipped(b *testing.B) {
	m := benchModel(100_000)
	m.split = true
	m.syntax = true
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = m.renderDiff(40)
	}
}

func BenchmarkRenderSplitSyntaxActive(b *testing.B) {
	m := benchModel(1_000)
	m.split = true
	m.syntax = true
	_ = m.renderDiff(40)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = m.renderDiff(40)
	}
}

func BenchmarkRenderSplitNavigation(b *testing.B) {
	m := benchModel(100_000)
	m.split = true
	m.syntax = true
	m.splitHunkCache = make(map[string]map[string]bool)
	m.splitNavCache = make(map[string]splitNav)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		m.moveLine(1)
	}
}

func BenchmarkSplitMoveAndRenderSyntaxActive(b *testing.B) {
	m := benchModel(1_000)
	m.split = true
	m.syntax = true
	_ = m.renderDiff(40)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		m.moveLine(1)
		_ = m.renderDiff(40)
	}
}

func BenchmarkSplitReplacementMoveAndRenderSyntaxActive(b *testing.B) {
	m := benchReplacementModel(1_000)
	m.split = true
	m.syntax = true
	_ = m.renderDiff(40)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		m.moveLine(1)
		_ = m.renderDiff(40)
	}
}

func BenchmarkSplitReplacementUniqueMoveAndRenderSyntaxActive(b *testing.B) {
	m := benchReplacementUniqueModel(1_000)
	m.split = true
	m.syntax = true
	_ = m.renderDiff(40)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		m.moveLine(1)
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
		store:          &annotate.Store{},
		session:        review.NewSession([]diff.File{file}),
		width:          120,
		height:         40,
		syntaxCache:    make(map[string]string),
		splitHunkCache: make(map[string]map[string]bool),
		splitNavCache:  make(map[string]splitNav),
	}
}

func benchReplacementModel(pairs int) Model {
	diffLines := make([]diff.Line, 0, pairs*2)
	for i := 0; i < pairs; i++ {
		diffLines = append(diffLines,
			diff.Line{Kind: diff.Delete, OldNo: i + 1, Text: "-func example() { return oldValue }"},
			diff.Line{Kind: diff.Add, NewNo: i + 1, Text: "+func example() { return newValue }"},
		)
	}
	file := diff.File{NewPath: "big.go", Hunks: []diff.Hunk{{Header: "@@ -1 +1 @@", Lines: diffLines}}}
	return Model{
		store:          &annotate.Store{},
		session:        review.NewSession([]diff.File{file}),
		width:          120,
		height:         40,
		syntaxCache:    make(map[string]string),
		splitHunkCache: make(map[string]map[string]bool),
		splitNavCache:  make(map[string]splitNav),
	}
}

func benchReplacementUniqueModel(pairs int) Model {
	diffLines := make([]diff.Line, 0, pairs*2)
	for i := 0; i < pairs; i++ {
		diffLines = append(diffLines,
			diff.Line{Kind: diff.Delete, OldNo: i + 1, Text: fmt.Sprintf("-func example%d() { return %d }", i, i)},
			diff.Line{Kind: diff.Add, NewNo: i + 1, Text: fmt.Sprintf("+func example%d() { return %d }", i, i+1)},
		)
	}
	file := diff.File{NewPath: "big.go", Hunks: []diff.Hunk{{Header: "@@ -1 +1 @@", Lines: diffLines}}}
	return Model{
		store:          &annotate.Store{},
		session:        review.NewSession([]diff.File{file}),
		width:          120,
		height:         40,
		syntaxCache:    make(map[string]string),
		splitHunkCache: make(map[string]map[string]bool),
		splitNavCache:  make(map[string]splitNav),
	}
}
