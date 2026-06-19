package annotate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	gh "github.com/owenps/tdiff/internal/github"
)

type Side string

const (
	SideOld Side = "old"
	SideNew Side = "new"
)

type Source string

const (
	SourceLocal  Source = "local"
	SourceGitHub Source = "github"
)

type Annotation struct {
	ID         string          `json:"id"`
	Path       string          `json:"path"`
	Side       Side            `json:"side"`
	Line       int             `json:"line"`
	LineStart  int             `json:"line_start,omitempty"`
	LineEnd    int             `json:"line_end,omitempty"`
	HunkHeader string          `json:"hunk_header"`
	Context    string          `json:"context"`
	Body       string          `json:"body"`
	Replies    []Reply         `json:"replies,omitempty"`
	Status     string          `json:"status"`
	Source     Source          `json:"source,omitempty"`
	DiffHash   string          `json:"diff_hash"`
	Outdated   bool            `json:"outdated"`
	GitHub     *GitHubMetadata `json:"github,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

type Reply struct {
	ID              string    `json:"id,omitempty"`
	Body            string    `json:"body"`
	AuthorLogin     string    `json:"author_login,omitempty"`
	AuthorName      string    `json:"author_name,omitempty"`
	AuthorAvatarURL string    `json:"author_avatar_url,omitempty"`
	URL             string    `json:"url,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type GitHubMetadata struct {
	Owner           string `json:"owner"`
	Repo            string `json:"repo"`
	PRNumber        int    `json:"pr_number"`
	ThreadID        string `json:"thread_id"`
	CommentID       string `json:"comment_id,omitempty"`
	URL             string `json:"url,omitempty"`
	Side            string `json:"side,omitempty"`
	StartSide       string `json:"start_side,omitempty"`
	CommitID        string `json:"commit_id,omitempty"`
	AuthorLogin     string `json:"author_login,omitempty"`
	AuthorName      string `json:"author_name,omitempty"`
	AuthorAvatarURL string `json:"author_avatar_url,omitempty"`
	Resolved        bool   `json:"resolved,omitempty"`
	Outdated        bool   `json:"outdated,omitempty"`
}

type ViewedFile struct {
	Path     string    `json:"path"`
	DiffHash string    `json:"diff_hash"`
	ViewedAt time.Time `json:"viewed_at"`
}

type Store struct {
	GitHub      *gh.AttachedPR `json:"github,omitempty"`
	Annotations []Annotation   `json:"annotations"`
	Viewed      []ViewedFile   `json:"viewed"`
	path        string
}

func Open(gitRoot string) (*Store, error) {
	gitDir, err := resolveGitDir(gitRoot)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(gitDir, "tdiff", "annotations.json")
	store := &Store{path: path}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return store, nil
		}
		return nil, err
	}
	if err := decodeStore(b, store); err != nil {
		return nil, err
	}
	store.path = path
	return store, nil
}

func decodeStore(b []byte, store *Store) error {
	var payload struct {
		GitHub      *gh.AttachedPR `json:"github"`
		Annotations []Annotation   `json:"annotations"`
		Viewed      []ViewedFile   `json:"viewed"`
	}
	if err := json.Unmarshal(b, &payload); err != nil {
		return err
	}
	store.GitHub = payload.GitHub
	store.Annotations = payload.Annotations
	store.Viewed = payload.Viewed
	return nil
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

func (s *Store) Add(n Annotation) error {
	now := time.Now()
	if n.ID == "" {
		n.ID = fmt.Sprintf("%d", now.UnixNano())
	}
	if n.Status == "" {
		n.Status = "open"
	}
	if n.Source == "" {
		n.Source = SourceLocal
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
	s.Annotations = append(s.Annotations, n)
	return s.Save()
}

func (s *Store) UpdateBody(id, body string) error {
	for i := range s.Annotations {
		if s.Annotations[i].ID == id {
			s.Annotations[i].Body = body
			s.Annotations[i].UpdatedAt = time.Now()
			return s.Save()
		}
	}
	return fmt.Errorf("annotation not found")
}

func (s *Store) Delete(id string) error {
	for i := range s.Annotations {
		if s.Annotations[i].ID == id {
			s.Annotations = append(s.Annotations[:i], s.Annotations[i+1:]...)
			return s.Save()
		}
	}
	return fmt.Errorf("annotation not found")
}

func (s *Store) AnnotationsFor(path string) []Annotation {
	var out []Annotation
	for _, n := range s.Annotations {
		if n.Path == path && !n.Outdated && n.Status != "resolved" {
			out = append(out, normalize(n))
		}
	}
	return out
}

func normalize(n Annotation) Annotation {
	if n.Source == "" {
		n.Source = SourceLocal
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
	return n
}

func (s *Store) AttachGitHubPR(pr gh.AttachedPR) error {
	s.GitHub = &pr
	return s.Save()
}

func (s *Store) SyncGitHubAnnotations(pr gh.AttachedPR, threads []gh.Thread) (int, error) {
	seen := map[string]bool{}
	count := 0
	for _, thread := range threads {
		if thread.Outdated || len(thread.Comments) == 0 {
			continue
		}
		n := annotationFromGitHubThread(pr, thread)
		if n.ID == "" {
			continue
		}
		seen[n.GitHub.ThreadID] = true
		count++
		updated := false
		for i := range s.Annotations {
			if s.Annotations[i].Source == SourceGitHub && s.Annotations[i].GitHub != nil && s.Annotations[i].GitHub.ThreadID == n.GitHub.ThreadID {
				s.Annotations[i] = n
				updated = true
				break
			}
		}
		if !updated {
			s.Annotations = append(s.Annotations, n)
		}
	}
	filtered := s.Annotations[:0]
	for _, n := range s.Annotations {
		if n.Source == SourceGitHub && n.GitHub != nil && n.GitHub.Owner == pr.Owner && n.GitHub.Repo == pr.Repo && n.GitHub.PRNumber == pr.Number && !seen[n.GitHub.ThreadID] {
			continue
		}
		filtered = append(filtered, n)
	}
	s.Annotations = filtered
	return count, s.Save()
}

func annotationFromGitHubThread(pr gh.AttachedPR, thread gh.Thread) Annotation {
	first := thread.Comments[0]
	side := sideFromGitHub(thread.Side)
	lineStart := thread.StartLine
	if lineStart == 0 {
		lineStart = thread.Line
	}
	lineEnd := thread.Line
	if lineEnd == 0 {
		lineEnd = lineStart
	}
	status := "open"
	if thread.Resolved {
		status = "resolved"
	}
	replies := make([]Reply, 0, len(thread.Comments)-1)
	for _, c := range thread.Comments[1:] {
		replies = append(replies, Reply{ID: c.ID, Body: c.Body, AuthorLogin: c.Author.Login, AuthorName: c.Author.Name, AuthorAvatarURL: c.Author.AvatarURL, URL: c.URL, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt})
	}
	return Annotation{
		ID:        "github:" + thread.ID,
		Path:      thread.Path,
		Side:      side,
		Line:      lineStart,
		LineStart: lineStart,
		LineEnd:   lineEnd,
		Body:      first.Body,
		Replies:   replies,
		Status:    status,
		Source:    SourceGitHub,
		Outdated:  thread.Outdated,
		GitHub:    &GitHubMetadata{Owner: pr.Owner, Repo: pr.Repo, PRNumber: pr.Number, ThreadID: thread.ID, CommentID: first.ID, URL: first.URL, Side: thread.Side, StartSide: thread.StartSide, AuthorLogin: first.Author.Login, AuthorName: first.Author.Name, AuthorAvatarURL: first.Author.AvatarURL, Resolved: thread.Resolved, Outdated: thread.Outdated},
		CreatedAt: first.CreatedAt,
		UpdatedAt: first.UpdatedAt,
	}
}

func sideFromGitHub(side string) Side {
	if side == "LEFT" {
		return SideOld
	}
	return SideNew
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
	for _, n := range s.Annotations {
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
