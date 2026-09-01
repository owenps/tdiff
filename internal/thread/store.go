package thread

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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

type Actor string

const (
	ActorHuman  Actor = "human"
	ActorAgent  Actor = "agent"
	ActorGitHub Actor = "github"
	ActorSystem Actor = "system"
)

type Status string

const (
	StatusOpen     Status = "open"
	StatusResolved Status = "resolved"
)

type Review struct {
	ApprovedDiffHash string    `json:"approved_diff_hash,omitempty"`
	ApprovedAt       time.Time `json:"approved_at,omitempty"`
}

type Thread struct {
	ID            string          `json:"id"`
	Path          string          `json:"path"`
	Side          Side            `json:"side"`
	Line          int             `json:"line"`
	LineStart     int             `json:"line_start,omitempty"`
	LineEnd       int             `json:"line_end,omitempty"`
	HunkHeader    string          `json:"hunk_header"`
	Context       string          `json:"context"`
	Messages      []Message       `json:"messages,omitempty"`
	Status        Status          `json:"status"`
	Source        Source          `json:"source,omitempty"`
	DiffHash      string          `json:"diff_hash"`
	ReadMessageID string          `json:"-"`
	Outdated      bool            `json:"outdated"`
	GitHub        *GitHubMetadata `json:"github,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

type Message struct {
	ID              string    `json:"id,omitempty"`
	Actor           Actor     `json:"actor"`
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

type Event struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	ThreadID  string    `json:"thread_id,omitempty"`
	Source    Source    `json:"source,omitempty"`
	Actor     Actor     `json:"actor,omitempty"`
	Status    Status    `json:"status,omitempty"`
	Path      string    `json:"path,omitempty"`
	Side      Side      `json:"side,omitempty"`
	LineStart int       `json:"line_start,omitempty"`
	LineEnd   int       `json:"line_end,omitempty"`
	DiffHash  string    `json:"diff_hash,omitempty"`
	Body      string    `json:"body,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type Store struct {
	Review      Review            `json:"review"`
	GitHub      *gh.AttachedPR    `json:"github,omitempty"`
	Threads     []Thread          `json:"threads"`
	Viewed      []ViewedFile      `json:"viewed"`
	ThreadReads map[string]string `json:"thread_reads,omitempty"`

	path       string
	eventsPath string
}

func Open(gitRoot string) (*Store, error) {
	gitDir, err := resolveGitDir(gitRoot)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(gitDir, "tdiff", "review.json")
	store := &Store{path: path, eventsPath: filepath.Join(gitDir, "tdiff", "events.jsonl")}
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
	store.eventsPath = filepath.Join(gitDir, "tdiff", "events.jsonl")
	return store, nil
}

func decodeStore(b []byte, store *Store) error {
	var payload struct {
		Review      Review            `json:"review"`
		GitHub      *gh.AttachedPR    `json:"github"`
		Threads     []Thread          `json:"threads"`
		Viewed      []ViewedFile      `json:"viewed"`
		ThreadReads map[string]string `json:"thread_reads"`
	}
	if err := json.Unmarshal(b, &payload); err != nil {
		return err
	}
	store.Review = payload.Review
	store.GitHub = payload.GitHub
	store.Threads = payload.Threads
	store.Viewed = payload.Viewed
	store.ThreadReads = payload.ThreadReads
	store.applyThreadReads()
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

func (s *Store) Path() string       { return s.path }
func (s *Store) EventsPath() string { return s.eventsPath }

func (s *Store) Save() error {
	s.captureThreadReads()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, append(b, '\n'), 0o644)
}

func (s *Store) Add(t Thread) error {
	now := time.Now()
	t = normalize(t)
	if t.ID == "" {
		t.ID = s.nextThreadID()
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = now
	}
	t.UpdatedAt = now
	for i := range t.Messages {
		if t.Messages[i].ID == "" {
			t.Messages[i].ID = fmt.Sprintf("M%d", now.UnixNano()+int64(i))
		}
		if t.Messages[i].Actor == "" {
			t.Messages[i].Actor = ActorHuman
		}
		if t.Messages[i].CreatedAt.IsZero() {
			t.Messages[i].CreatedAt = now
		}
		if t.Messages[i].UpdatedAt.IsZero() {
			t.Messages[i].UpdatedAt = t.Messages[i].CreatedAt
		}
	}
	s.invalidateApprovalFor(t.DiffHash)
	s.Threads = append(s.Threads, t)
	if err := s.Save(); err != nil {
		return err
	}
	body := ""
	actor := ActorHuman
	if len(t.Messages) > 0 {
		body = t.Messages[0].Body
		actor = t.Messages[0].Actor
	}
	return s.appendEvent(Event{Type: "thread.created", ThreadID: t.ID, Actor: actor, Path: t.Path, Side: t.Side, LineStart: t.LineStart, LineEnd: t.LineEnd, DiffHash: t.DiffHash, Body: body})
}

func (s *Store) Thread(id string) (Thread, bool) {
	for _, t := range s.Threads {
		if t.ID == id {
			return normalize(t), true
		}
	}
	return Thread{}, false
}

func (s *Store) UpdateLatestMessage(id, body string) error {
	for i := range s.Threads {
		if s.Threads[i].ID != id {
			continue
		}
		if !CanEditLatestMessage(s.Threads[i]) {
			return fmt.Errorf("can only edit latest local message")
		}
		now := time.Now()
		msgIdx := len(s.Threads[i].Messages) - 1
		s.Threads[i].Messages[msgIdx].Body = body
		s.Threads[i].Messages[msgIdx].UpdatedAt = now
		s.Threads[i].UpdatedAt = now
		s.invalidateApprovalFor(s.Threads[i].DiffHash)
		if err := s.Save(); err != nil {
			return err
		}
		return s.appendEvent(Event{Type: "thread.edited", ThreadID: id, Actor: s.Threads[i].Messages[msgIdx].Actor, Body: body})
	}
	return fmt.Errorf("thread not found")
}

func (s *Store) Delete(id string) error {
	for i := range s.Threads {
		if s.Threads[i].ID == id {
			s.Threads = append(s.Threads[:i], s.Threads[i+1:]...)
			delete(s.ThreadReads, id)
			if err := s.Save(); err != nil {
				return err
			}
			return s.appendEvent(Event{Type: "thread.deleted", ThreadID: id})
		}
	}
	return fmt.Errorf("thread not found")
}

func (s *Store) ClearThreads() (int, error) {
	count := len(s.Threads)
	s.Threads = nil
	s.ThreadReads = map[string]string{}
	if err := s.Save(); err != nil {
		return count, err
	}
	return count, s.appendEvent(Event{Type: "threads.cleared"})
}

func (s *Store) ThreadsFor(path string) []Thread {
	var out []Thread
	for _, t := range s.Threads {
		t = normalize(t)
		if t.Path == path && !t.Outdated && t.Status != StatusResolved {
			out = append(out, t)
		}
	}
	return out
}

func (s *Store) Reply(id string, msg Message) error {
	for i := range s.Threads {
		if s.Threads[i].ID != id {
			continue
		}
		now := time.Now()
		if msg.ID == "" {
			msg.ID = fmt.Sprintf("M%d", now.UnixNano())
		}
		if msg.Actor == "" {
			msg.Actor = ActorAgent
		}
		if msg.CreatedAt.IsZero() {
			msg.CreatedAt = now
		}
		if msg.UpdatedAt.IsZero() {
			msg.UpdatedAt = msg.CreatedAt
		}
		s.Threads[i].Messages = append(s.Threads[i].Messages, msg)
		s.Threads[i].UpdatedAt = now
		if msg.Actor == ActorHuman {
			s.Threads[i].ReadMessageID = msg.ID
			s.invalidateApprovalFor(s.Threads[i].DiffHash)
		}
		if err := s.Save(); err != nil {
			return err
		}
		return s.appendEvent(Event{Type: "thread.replied", ThreadID: id, Actor: msg.Actor, Body: msg.Body})
	}
	return fmt.Errorf("thread not found")
}

func (s *Store) SetStatus(id string, status Status, actor Actor) error {
	if status == "" {
		status = StatusOpen
	}
	if actor == "" {
		actor = ActorHuman
	}
	for i := range s.Threads {
		if s.Threads[i].ID != id {
			continue
		}
		s.Threads[i].Status = status
		s.Threads[i].UpdatedAt = time.Now()
		if status == StatusOpen && actor == ActorHuman {
			s.invalidateApprovalFor(s.Threads[i].DiffHash)
		}
		if err := s.Save(); err != nil {
			return err
		}
		return s.appendEvent(Event{Type: "thread.status_changed", ThreadID: id, Actor: actor, Status: status})
	}
	return fmt.Errorf("thread not found")
}

func (s *Store) Resolve(id string, actor Actor) error { return s.SetStatus(id, StatusResolved, actor) }
func (s *Store) Reopen(id string, actor Actor) error  { return s.SetStatus(id, StatusOpen, actor) }

func (s *Store) Approve(diffHash string) error {
	now := time.Now()
	s.Review.ApprovedDiffHash = diffHash
	s.Review.ApprovedAt = now
	if err := s.Save(); err != nil {
		return err
	}
	return s.appendEvent(Event{Type: "review.approved", Actor: ActorHuman, DiffHash: diffHash})
}

func (s *Store) Unapprove(diffHash string) error {
	s.Review = Review{}
	if err := s.Save(); err != nil {
		return err
	}
	return s.appendEvent(Event{Type: "review.unapproved", Actor: ActorHuman, DiffHash: diffHash})
}

func (s *Store) ReviewStatus(diffHash string) string {
	if diffHash != "" && s.Review.ApprovedDiffHash == diffHash {
		return "approved"
	}
	return "pending"
}

func (s *Store) MarkThreadRead(id string) error {
	for i := range s.Threads {
		if s.Threads[i].ID != id {
			continue
		}
		last := LastMessage(s.Threads[i])
		if last.ID == "" || s.Threads[i].ReadMessageID == last.ID {
			return nil
		}
		s.Threads[i].ReadMessageID = last.ID
		s.setThreadRead(id, last.ID)
		if s.path == "" {
			return nil
		}
		return s.Save()
	}
	return fmt.Errorf("thread not found")
}

func (s *Store) invalidateApprovalFor(diffHash string) {
	if diffHash != "" && s.Review.ApprovedDiffHash == diffHash {
		s.Review = Review{}
	}
}

func (s *Store) applyThreadReads() {
	if len(s.ThreadReads) == 0 {
		return
	}
	for i := range s.Threads {
		s.Threads[i].ReadMessageID = s.ThreadReads[s.Threads[i].ID]
	}
}

func (s *Store) captureThreadReads() {
	for _, t := range s.Threads {
		if t.ReadMessageID != "" {
			s.setThreadRead(t.ID, t.ReadMessageID)
		}
	}
}

func (s *Store) setThreadRead(id, messageID string) {
	if id == "" || messageID == "" {
		return
	}
	if s.ThreadReads == nil {
		s.ThreadReads = make(map[string]string)
	}
	s.ThreadReads[id] = messageID
}

func (s *Store) nextThreadID() string {
	maxID := int64(0)
	seen := map[string]bool{}
	for _, t := range s.Threads {
		seen[t.ID] = true
		if len(t.ID) < 2 || t.ID[0] != 'T' {
			continue
		}
		n, err := strconv.ParseInt(strings.ToLower(t.ID[1:]), 36, 64)
		if err == nil && n > maxID {
			maxID = n
		}
	}
	for n := maxID + 1; ; n++ {
		id := "T" + strings.ToUpper(strconv.FormatInt(n, 36))
		if !seen[id] {
			return id
		}
	}
}

func normalize(t Thread) Thread {
	if t.Source == "" {
		t.Source = SourceLocal
	}
	if t.Status == "" {
		t.Status = StatusOpen
	}
	if t.LineStart == 0 {
		t.LineStart = t.Line
	}
	if t.LineEnd == 0 {
		t.LineEnd = t.LineStart
	}
	if t.Line == 0 {
		t.Line = t.LineStart
	}
	return t
}

func FirstMessage(t Thread) Message {
	if len(t.Messages) == 0 {
		return Message{}
	}
	return t.Messages[0]
}

func Body(t Thread) string { return FirstMessage(t).Body }

func LastMessage(t Thread) Message {
	if len(t.Messages) == 0 {
		return Message{}
	}
	return t.Messages[len(t.Messages)-1]
}

func CanEditLatestMessage(t Thread) bool {
	return t.Source != SourceGitHub && len(t.Messages) > 0 && LastMessage(t).Actor == ActorHuman
}

func LastActor(t Thread) Actor {
	if len(t.Messages) == 0 {
		return ""
	}
	return t.Messages[len(t.Messages)-1].Actor
}

func UnreadForHuman(t Thread) bool {
	last := LastMessage(t)
	return last.ID != "" && last.Actor != ActorHuman && t.ReadMessageID != last.ID
}

func (s *Store) AttachGitHubPR(pr gh.AttachedPR) error {
	s.GitHub = &pr
	return s.Save()
}

func (s *Store) SyncGitHubThreads(pr gh.AttachedPR, threads []gh.Thread) (int, error) {
	seen := map[string]bool{}
	count := 0
	var events []Event
	for _, ghThread := range threads {
		if ghThread.Outdated || len(ghThread.Comments) == 0 {
			continue
		}
		t := threadFromGitHubThread(pr, ghThread)
		if t.ID == "" {
			continue
		}
		seen[t.GitHub.ThreadID] = true
		count++
		updated := false
		for i := range s.Threads {
			if s.Threads[i].Source == SourceGitHub && s.Threads[i].GitHub != nil && s.Threads[i].GitHub.ThreadID == t.GitHub.ThreadID {
				events = append(events, githubReplyEvents(s.Threads[i], t)...)
				t.ReadMessageID = s.Threads[i].ReadMessageID
				s.Threads[i] = t
				updated = true
				break
			}
		}
		if !updated {
			events = append(events, githubThreadCreatedEvent(t))
			s.Threads = append(s.Threads, t)
		}
	}
	filtered := s.Threads[:0]
	for _, t := range s.Threads {
		if t.Source == SourceGitHub && t.GitHub != nil && t.GitHub.Owner == pr.Owner && t.GitHub.Repo == pr.Repo && t.GitHub.PRNumber == pr.Number && !seen[t.GitHub.ThreadID] {
			continue
		}
		filtered = append(filtered, t)
	}
	s.Threads = filtered
	if err := s.Save(); err != nil {
		return count, err
	}
	for _, e := range events {
		if err := s.appendEvent(e); err != nil {
			return count, err
		}
	}
	return count, nil
}

func githubThreadCreatedEvent(t Thread) Event {
	msg := firstMessage(t)
	return Event{Type: "thread.created", ThreadID: t.ID, Source: SourceGitHub, Actor: actorFromGitHubMessage(msg), Path: t.Path, Side: t.Side, LineStart: t.LineStart, LineEnd: t.LineEnd, DiffHash: t.DiffHash, Body: msg.Body}
}

func githubReplyEvents(previous, current Thread) []Event {
	seen := map[string]bool{}
	for _, msg := range previous.Messages {
		if msg.ID != "" {
			seen[msg.ID] = true
		}
	}
	var events []Event
	for _, msg := range current.Messages {
		if msg.ID == "" || seen[msg.ID] {
			continue
		}
		events = append(events, Event{Type: "thread.replied", ThreadID: current.ID, Source: SourceGitHub, Actor: actorFromGitHubMessage(msg), Path: current.Path, Side: current.Side, LineStart: current.LineStart, LineEnd: current.LineEnd, DiffHash: current.DiffHash, Body: msg.Body})
	}
	return events
}

func firstMessage(t Thread) Message {
	if len(t.Messages) == 0 {
		return Message{}
	}
	return t.Messages[0]
}

func actorFromGitHubMessage(msg Message) Actor {
	if msg.AuthorLogin != "" {
		return Actor(msg.AuthorLogin)
	}
	return ActorGitHub
}

func threadFromGitHubThread(pr gh.AttachedPR, ghThread gh.Thread) Thread {
	first := ghThread.Comments[0]
	side := sideFromGitHub(ghThread.Side)
	lineStart := ghThread.StartLine
	if lineStart == 0 {
		lineStart = ghThread.Line
	}
	lineEnd := ghThread.Line
	if lineEnd == 0 {
		lineEnd = lineStart
	}
	status := StatusOpen
	if ghThread.Resolved {
		status = StatusResolved
	}
	messages := make([]Message, 0, len(ghThread.Comments))
	for _, c := range ghThread.Comments {
		messages = append(messages, Message{ID: c.ID, Actor: ActorGitHub, Body: c.Body, AuthorLogin: c.Author.Login, AuthorName: c.Author.Name, AuthorAvatarURL: c.Author.AvatarURL, URL: c.URL, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt})
	}
	return Thread{
		ID:        "github:" + ghThread.ID,
		Path:      ghThread.Path,
		Side:      side,
		Line:      lineStart,
		LineStart: lineStart,
		LineEnd:   lineEnd,
		Messages:  messages,
		Status:    status,
		Source:    SourceGitHub,
		Outdated:  ghThread.Outdated,
		GitHub:    &GitHubMetadata{Owner: pr.Owner, Repo: pr.Repo, PRNumber: pr.Number, ThreadID: ghThread.ID, CommentID: first.ID, URL: first.URL, Side: ghThread.Side, StartSide: ghThread.StartSide, AuthorLogin: first.Author.Login, AuthorName: first.Author.Name, AuthorAvatarURL: first.Author.AvatarURL, Resolved: ghThread.Resolved, Outdated: ghThread.Outdated},
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

func (s *Store) appendEvent(e Event) error {
	if s.eventsPath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.eventsPath), 0o755); err != nil {
		return err
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now()
	}
	if e.ID == "" {
		e.ID = fmt.Sprintf("E%d", e.CreatedAt.UnixNano())
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(s.eventsPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}
