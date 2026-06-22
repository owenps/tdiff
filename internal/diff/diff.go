package diff

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type File struct {
	OldPath string
	NewPath string
	Hunks   []Hunk
	Hash    string
}

type Hunk struct {
	Header string
	Lines  []Line
}

type Line struct {
	Kind  Kind
	OldNo int
	NewNo int
	Text  string
}

type Kind int

const (
	Context Kind = iota
	Add
	Delete
	Meta
)

var hunkRE = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

func Parse(raw string) ([]File, error) {
	var files []File
	var cur *File
	var hunk *Hunk
	var rawLines []string
	oldLine, newLine := 0, 0
	finishFile := func() {
		if cur != nil {
			cur.Hash = hashString(strings.Join(rawLines, "\n"))
		}
	}

	for _, line := range strings.Split(strings.TrimSuffix(raw, "\n"), "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			finishFile()
			files = append(files, File{})
			cur = &files[len(files)-1]
			hunk = nil
			rawLines = []string{line}
			parts := strings.Fields(line)
			if len(parts) >= 4 {
				cur.OldPath = strings.TrimPrefix(parts[2], "a/")
				cur.NewPath = strings.TrimPrefix(parts[3], "b/")
			}
			continue
		}
		if cur != nil {
			rawLines = append(rawLines, line)
		}
		switch {
		case cur == nil:
			continue
		case strings.HasPrefix(line, "--- "):
			cur.OldPath = cleanPath(strings.TrimPrefix(line, "--- "))
		case strings.HasPrefix(line, "+++ "):
			cur.NewPath = cleanPath(strings.TrimPrefix(line, "+++ "))
		case strings.HasPrefix(line, "@@ "):
			m := hunkRE.FindStringSubmatch(line)
			if len(m) != 3 {
				return nil, fmt.Errorf("parse hunk header %q", line)
			}
			oldLine, _ = strconv.Atoi(m[1])
			newLine, _ = strconv.Atoi(m[2])
			cur.Hunks = append(cur.Hunks, Hunk{Header: line})
			hunk = &cur.Hunks[len(cur.Hunks)-1]
		case hunk != nil:
			if line == `\ No newline at end of file` {
				hunk.Lines = append(hunk.Lines, Line{Kind: Meta, Text: line})
				continue
			}

			kind := Context
			oldNo, newNo := oldLine, newLine
			text := line
			if len(line) > 0 {
				switch line[0] {
				case '+':
					kind = Add
					oldNo = 0
					newLine++
				case '-':
					kind = Delete
					newNo = 0
					oldLine++
				case ' ':
					oldLine++
					newLine++
				default:
					kind = Meta
					oldNo = 0
					newNo = 0
				}
			}
			hunk.Lines = append(hunk.Lines, Line{Kind: kind, OldNo: oldNo, NewNo: newNo, Text: text})
		}
	}
	finishFile()

	return files, nil
}

func (f File) Path() string {
	if f.NewPath != "" && f.NewPath != "/dev/null" {
		return f.NewPath
	}
	return f.OldPath
}

func FileHash(f File) string {
	if f.Hash != "" {
		return f.Hash
	}
	var b strings.Builder
	fmt.Fprintf(&b, "old=%s\x00new=%s\x00", f.OldPath, f.NewPath)
	for _, h := range f.Hunks {
		fmt.Fprintf(&b, "hunk=%s\x00", h.Header)
		for _, l := range h.Lines {
			fmt.Fprintf(&b, "line=%d:%d:%d:%s\x00", l.Kind, l.OldNo, l.NewNo, l.Text)
		}
	}
	return hashString(b.String())
}

func hashString(s string) string {
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func cleanPath(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "a/")
	s = strings.TrimPrefix(s, "b/")
	return s
}
