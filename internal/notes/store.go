package notes

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Side string

const (
	SideOld Side = "old"
	SideNew Side = "new"
)

type Note struct {
	ID         string    `json:"id"`
	Path       string    `json:"path"`
	Side       Side      `json:"side"`
	Line       int       `json:"line"`
	LineStart  int       `json:"line_start,omitempty"`
	LineEnd    int       `json:"line_end,omitempty"`
	HunkHeader string    `json:"hunk_header"`
	Context    string    `json:"context"`
	Body       string    `json:"body"`
	Status     string    `json:"status"`
	DiffHash   string    `json:"diff_hash"`
	Outdated   bool      `json:"outdated"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type ViewedFile struct {
	Path     string    `json:"path"`
	DiffHash string    `json:"diff_hash"`
	ViewedAt time.Time `json:"viewed_at"`
}

type Store struct {
	Notes  []Note       `json:"notes"`
	Viewed []ViewedFile `json:"viewed"`
	path   string
}

func Open(gitRoot string) (*Store, error) {
	gitDir, err := resolveGitDir(gitRoot)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(gitDir, "tdiff", "notes.json")
	store := &Store{path: path}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return store, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(b, store); err != nil {
		return nil, err
	}
	store.path = path
	return store, nil
}

func resolveGitDir(gitRoot string) (string, error) {
	gitPath := filepath.Join(gitRoot, ".git")
	info, err := os.Stat(gitPath)
	if err == nil && info.IsDir() {
		return gitPath, nil
	}
	if err != nil {
		return "", err
	}

	b, err := os.ReadFile(gitPath)
	if err != nil {
		return "", err
	}
	const prefix = "gitdir:"
	text := strings.TrimSpace(string(b))
	if !strings.HasPrefix(text, prefix) {
		return "", fmt.Errorf("invalid .git file: %s", gitPath)
	}
	gitDir := filepath.Clean(strings.TrimSpace(strings.TrimPrefix(text, prefix)))
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(gitRoot, gitDir)
	}
	return gitDir, nil
}

func (s *Store) Save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, append(b, '\n'), 0o644)
}

func (s *Store) Add(n Note) error {
	now := time.Now()
	if n.ID == "" {
		n.ID = fmt.Sprintf("%d", now.UnixNano())
	}
	if n.Status == "" {
		n.Status = "open"
	}
	if n.LineStart == 0 {
		n.LineStart = n.Line
	}
	if n.LineEnd == 0 {
		n.LineEnd = n.LineStart
	}
	if n.Line == 0 {
		n.Line = n.LineStart
	}
	n.CreatedAt = now
	n.UpdatedAt = now
	s.Notes = append(s.Notes, n)
	return s.Save()
}

func (s *Store) UpdateBody(id, body string) error {
	for i := range s.Notes {
		if s.Notes[i].ID == id {
			s.Notes[i].Body = body
			s.Notes[i].UpdatedAt = time.Now()
			return s.Save()
		}
	}
	return fmt.Errorf("annotation not found")
}

func (s *Store) Delete(id string) error {
	for i := range s.Notes {
		if s.Notes[i].ID == id {
			s.Notes = append(s.Notes[:i], s.Notes[i+1:]...)
			return s.Save()
		}
	}
	return fmt.Errorf("annotation not found")
}

func (s *Store) NotesFor(path string) []Note {
	var out []Note
	for _, n := range s.Notes {
		if n.Path == path && !n.Outdated {
			out = append(out, normalize(n))
		}
	}
	return out
}

func normalize(n Note) Note {
	if n.LineStart == 0 {
		n.LineStart = n.Line
	}
	if n.LineEnd == 0 {
		n.LineEnd = n.LineStart
	}
	if n.Line == 0 {
		n.Line = n.LineStart
	}
	return n
}

func (s *Store) MarkViewed(path, diffHash string) error {
	for i := range s.Viewed {
		if s.Viewed[i].Path == path {
			s.Viewed[i].DiffHash = diffHash
			s.Viewed[i].ViewedAt = time.Now()
			return s.Save()
		}
	}
	s.Viewed = append(s.Viewed, ViewedFile{Path: path, DiffHash: diffHash, ViewedAt: time.Now()})
	return s.Save()
}

func (s *Store) ClearViewed(path string) error {
	for i := range s.Viewed {
		if s.Viewed[i].Path == path {
			s.Viewed = append(s.Viewed[:i], s.Viewed[i+1:]...)
			return s.Save()
		}
	}
	return nil
}

func (s *Store) IsViewed(path, diffHash string) bool {
	for _, v := range s.Viewed {
		if v.Path == path && v.DiffHash == diffHash {
			return true
		}
	}
	return false
}

func (s *Store) ExportMarkdown() string {
	out := "# tdiff annotations\n\n"
	for _, n := range s.Notes {
		if n.Outdated || n.Status == "resolved" {
			continue
		}
		n = normalize(n)
		loc := fmt.Sprintf("%s:%d", n.Path, n.LineStart)
		if n.LineEnd != n.LineStart {
			loc = fmt.Sprintf("%s:%d-%d", n.Path, n.LineStart, n.LineEnd)
		}
		out += fmt.Sprintf("- [ ] `%s` (%s) %s\n", loc, n.Side, n.Body)
		if n.Context != "" {
			out += fmt.Sprintf("\n  ```diff\n  %s\n  ```\n", n.Context)
		}
	}
	return out
}
