package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/owenps/tdiff/internal/app"
	"github.com/owenps/tdiff/internal/diff"
	"github.com/owenps/tdiff/internal/git"
	gh "github.com/owenps/tdiff/internal/github"
	"github.com/owenps/tdiff/internal/snapshot"
	"github.com/owenps/tdiff/internal/thread"
)

type diffFlags struct {
	base             string
	mode             git.Mode
	ignoreWhitespace bool
}

type loadedReview struct {
	store *thread.Store
	snap  snapshot.Snapshot
}

type threadView struct {
	thread.Thread
	Freshness string       `json:"freshness"`
	LastActor thread.Actor `json:"last_actor,omitempty"`
}

type reviewSummary struct {
	Status           string     `json:"status"`
	DiffHash         string     `json:"diff_hash"`
	ApprovedDiffHash string     `json:"approved_diff_hash,omitempty"`
	ApprovedAt       *time.Time `json:"approved_at,omitempty"`
	OpenThreads      int        `json:"open_threads"`
	CurrentThreads   int        `json:"current_threads"`
	StaleThreads     int        `json:"stale_threads"`
	TotalThreads     int        `json:"total_threads"`
}

type reviewContext struct {
	Review  reviewSummary       `json:"review"`
	GitHub  *gh.AttachedPR      `json:"github,omitempty"`
	Viewed  []thread.ViewedFile `json:"viewed,omitempty"`
	Files   []fileSummary       `json:"files"`
	Threads []threadView        `json:"threads"`
}

type fileSummary struct {
	Path      string `json:"path"`
	HunkCount int    `json:"hunk_count"`
}

func Run() error {
	ctx := context.Background()
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "review":
			return runReview(ctx, os.Args[2:])
		case "thread":
			return runThread(ctx, os.Args[2:])
		case "events":
			return runEvents(ctx, os.Args[2:])
		case "export":
			return fmt.Errorf("tdiff export removed; use `tdiff review context --json`")
		}
	}
	return runTUI(ctx, os.Args[1:])
}

func runTUI(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("tdiff", flag.ExitOnError)
	base := fs.String("base", "", "base branch/ref for branch diff")
	staged := fs.Bool("staged", false, "show staged diff")
	unstaged := fs.Bool("unstaged", false, "show unstaged diff")
	ignoreWhitespace := fs.Bool("ignore-space-change", false, "ignore whitespace changes")
	offline := fs.Bool("offline", false, "disable network/external integrations")
	if err := fs.Parse(args); err != nil {
		return err
	}
	mode := modeFromFlags(*staged, *unstaged)
	m, err := app.New(ctx, app.Config{Base: *base, Mode: mode, IgnoreWhitespace: *ignoreWhitespace, Offline: *offline})
	if err != nil {
		return err
	}
	_, err = tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

func runReview(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: tdiff review status|context|approve|unapprove|watch")
	}
	sub := args[0]
	switch sub {
	case "status":
		dfs := newDiffFlagSet("tdiff review status")
		jsonOut := dfs.fs.Bool("json", false, "emit JSON")
		if err := dfs.fs.Parse(args[1:]); err != nil {
			return err
		}
		loaded, err := loadReview(ctx, dfs.opts())
		if err != nil {
			return err
		}
		summary := summarizeReview(loaded.store, loaded.snap)
		if *jsonOut {
			return writeJSON(summary)
		}
		fmt.Printf("review %s · %d open current threads · %d stale · %s\n", summary.Status, summary.CurrentThreads, summary.StaleThreads, summary.DiffHash)
		return nil
	case "context":
		dfs := newDiffFlagSet("tdiff review context")
		jsonOut := dfs.fs.Bool("json", false, "emit JSON")
		if err := dfs.fs.Parse(args[1:]); err != nil {
			return err
		}
		loaded, err := loadReview(ctx, dfs.opts())
		if err != nil {
			return err
		}
		ctx := buildReviewContext(loaded.store, loaded.snap)
		if *jsonOut {
			return writeJSON(ctx)
		}
		fmt.Printf("review %s · files %d · threads %d\n", ctx.Review.Status, len(ctx.Files), len(ctx.Threads))
		return nil
	case "approve":
		dfs := newDiffFlagSet("tdiff review approve")
		force := dfs.fs.Bool("force", false, "approve even with open current threads")
		if err := dfs.fs.Parse(args[1:]); err != nil {
			return err
		}
		loaded, err := loadReview(ctx, dfs.opts())
		if err != nil {
			return err
		}
		summary := summarizeReview(loaded.store, loaded.snap)
		if summary.CurrentThreads > 0 && !*force {
			return fmt.Errorf("cannot approve: %d open current threads", summary.CurrentThreads)
		}
		if err := loaded.store.Approve(loaded.snap.Hash); err != nil {
			return err
		}
		fmt.Printf("approved %s\n", loaded.snap.Hash)
		return nil
	case "unapprove":
		dfs := newDiffFlagSet("tdiff review unapprove")
		if err := dfs.fs.Parse(args[1:]); err != nil {
			return err
		}
		loaded, err := loadReview(ctx, dfs.opts())
		if err != nil {
			return err
		}
		if err := loaded.store.Unapprove(loaded.snap.Hash); err != nil {
			return err
		}
		fmt.Printf("approval removed %s\n", loaded.snap.Hash)
		return nil
	case "watch":
		dfs := newDiffFlagSet("tdiff review watch")
		jsonOut := dfs.fs.Bool("json", false, "emit JSON")
		if err := dfs.fs.Parse(args[1:]); err != nil {
			return err
		}
		if !*jsonOut {
			return errors.New("review watch currently requires --json")
		}
		loaded, err := loadReview(ctx, dfs.opts())
		if err != nil {
			return err
		}
		return streamEvents(loaded.store.EventsPath(), true, true)
	default:
		return fmt.Errorf("unknown review command %q", sub)
	}
}

func runThread(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: tdiff thread list|show|add|reply|resolve|reopen")
	}
	sub := args[0]
	switch sub {
	case "list":
		dfs := newDiffFlagSet("tdiff thread list")
		jsonOut := dfs.fs.Bool("json", false, "emit JSON")
		if err := dfs.fs.Parse(args[1:]); err != nil {
			return err
		}
		loaded, err := loadReview(ctx, dfs.opts())
		if err != nil {
			return err
		}
		threads := threadViews(loaded.store.Threads, loaded.snap.Files)
		if *jsonOut {
			return writeJSON(struct {
				Threads []threadView `json:"threads"`
			}{Threads: threads})
		}
		for _, t := range threads {
			fmt.Printf("%s %s %s:%d %s\n", t.ID, t.Status, t.Path, t.LineStart, firstLine(thread.Body(t.Thread)))
		}
		return nil
	case "show":
		if len(args) < 2 {
			return errors.New("usage: tdiff thread show <id> [--json]")
		}
		dfs := newDiffFlagSet("tdiff thread show")
		jsonOut := dfs.fs.Bool("json", false, "emit JSON")
		if err := dfs.fs.Parse(args[2:]); err != nil {
			return err
		}
		loaded, err := loadReview(ctx, dfs.opts())
		if err != nil {
			return err
		}
		t, ok := loaded.store.Thread(args[1])
		if !ok {
			return fmt.Errorf("thread not found: %s", args[1])
		}
		view := threadView{Thread: t, Freshness: freshness(t, loaded.snap.Files), LastActor: thread.LastActor(t)}
		if *jsonOut {
			return writeJSON(view)
		}
		fmt.Printf("%s %s %s:%d\n%s\n", t.ID, t.Status, t.Path, t.LineStart, thread.Body(t))
		return nil
	case "add":
		dfs := newDiffFlagSet("tdiff thread add")
		file := dfs.fs.String("file", "", "file path")
		line := dfs.fs.Int("line", 0, "line number")
		start := dfs.fs.Int("start", 0, "start line")
		end := dfs.fs.Int("end", 0, "end line")
		sideText := dfs.fs.String("side", "new", "old or new")
		body := dfs.fs.String("body", "", "thread body")
		actorText := dfs.fs.String("actor", "human", "human or agent")
		if err := dfs.fs.Parse(args[1:]); err != nil {
			return err
		}
		bodyText, err := readBody(*body)
		if err != nil {
			return err
		}
		if *file == "" || bodyText == "" {
			return errors.New("thread add requires --file and --body")
		}
		if *start == 0 {
			*start = *line
		}
		if *end == 0 {
			*end = *start
		}
		if *start == 0 {
			return errors.New("thread add requires --line or --start")
		}
		side, err := parseSide(*sideText)
		if err != nil {
			return err
		}
		actor, err := parseActor(*actorText)
		if err != nil {
			return err
		}
		loaded, err := loadReview(ctx, dfs.opts())
		if err != nil {
			return err
		}
		target, err := targetFromSnapshot(loaded.snap.Files, *file, side, *start, *end)
		if err != nil {
			return err
		}
		t := thread.Thread{Path: *file, Side: side, Line: *start, LineStart: *start, LineEnd: *end, HunkHeader: target.hunkHeader, Context: target.context, DiffHash: loaded.snap.Hash, Messages: []thread.Message{{Actor: actor, Body: bodyText}}}
		if err := loaded.store.Add(t); err != nil {
			return err
		}
		created := loaded.store.Threads[len(loaded.store.Threads)-1]
		fmt.Printf("added thread %s\n", created.ID)
		return nil
	case "reply":
		if len(args) < 2 {
			return errors.New("usage: tdiff thread reply <id> --body ... [--actor agent]")
		}
		dfs := newDiffFlagSet("tdiff thread reply")
		body := dfs.fs.String("body", "", "reply body")
		actorText := dfs.fs.String("actor", "agent", "human or agent")
		if err := dfs.fs.Parse(args[2:]); err != nil {
			return err
		}
		bodyText, err := readBody(*body)
		if err != nil {
			return err
		}
		if bodyText == "" {
			return errors.New("thread reply requires --body")
		}
		actor, err := parseActor(*actorText)
		if err != nil {
			return err
		}
		loaded, err := loadReview(ctx, dfs.opts())
		if err != nil {
			return err
		}
		if err := loaded.store.Reply(args[1], thread.Message{Actor: actor, Body: bodyText}); err != nil {
			return err
		}
		fmt.Printf("replied to %s\n", args[1])
		return nil
	case "resolve", "reopen":
		if len(args) < 2 {
			return fmt.Errorf("usage: tdiff thread %s <id> [--actor human]", sub)
		}
		dfs := newDiffFlagSet("tdiff thread " + sub)
		actorText := dfs.fs.String("actor", "human", "human or agent")
		if err := dfs.fs.Parse(args[2:]); err != nil {
			return err
		}
		actor, err := parseActor(*actorText)
		if err != nil {
			return err
		}
		loaded, err := loadReview(ctx, dfs.opts())
		if err != nil {
			return err
		}
		if sub == "resolve" {
			err = loaded.store.Resolve(args[1], actor)
		} else {
			err = loaded.store.Reopen(args[1], actor)
		}
		if err != nil {
			return err
		}
		fmt.Printf("%s %s\n", sub, args[1])
		return nil
	default:
		return fmt.Errorf("unknown thread command %q", sub)
	}
}

func runEvents(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("tdiff events", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "emit JSON")
	follow := fs.Bool("follow", false, "follow new events")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*jsonOut {
		return errors.New("events currently requires --json")
	}
	repo, err := git.Open(ctx)
	if err != nil {
		return err
	}
	store, err := thread.Open(repo.Root)
	if err != nil {
		return err
	}
	return streamEvents(store.EventsPath(), *follow, true)
}

type diffFlagSet struct {
	fs               *flag.FlagSet
	base             *string
	staged           *bool
	unstaged         *bool
	ignoreWhitespace *bool
}

func newDiffFlagSet(name string) *diffFlagSet {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	return &diffFlagSet{
		fs:               fs,
		base:             fs.String("base", "", "base branch/ref for branch diff"),
		staged:           fs.Bool("staged", false, "show staged diff"),
		unstaged:         fs.Bool("unstaged", false, "show unstaged diff"),
		ignoreWhitespace: fs.Bool("ignore-space-change", false, "ignore whitespace changes"),
	}
}

func (d *diffFlagSet) opts() diffFlags {
	return diffFlags{base: *d.base, mode: modeFromFlags(*d.staged, *d.unstaged), ignoreWhitespace: *d.ignoreWhitespace}
}

func modeFromFlags(staged, unstaged bool) git.Mode {
	mode := git.ModeBranch
	if staged {
		mode = git.ModeStaged
	}
	if unstaged {
		mode = git.ModeUnstaged
	}
	return mode
}

func loadReview(ctx context.Context, opts diffFlags) (loadedReview, error) {
	repo, err := git.Open(ctx)
	if err != nil {
		return loadedReview{}, err
	}
	store, err := thread.Open(repo.Root)
	if err != nil {
		return loadedReview{}, err
	}
	snap, err := snapshot.Load(ctx, repo, git.DiffOptions{Mode: opts.mode, Base: opts.base, IgnoreWhitespace: opts.ignoreWhitespace})
	if err != nil {
		return loadedReview{}, err
	}
	return loadedReview{store: store, snap: snap}, nil
}

func summarizeReview(store *thread.Store, snap snapshot.Snapshot) reviewSummary {
	current, stale, open := 0, 0, 0
	for _, t := range store.Threads {
		if t.Status == thread.StatusResolved {
			continue
		}
		open++
		if isThreadCurrent(t, snap.Files) {
			current++
		} else {
			stale++
		}
	}
	var approvedAt *time.Time
	if !store.Review.ApprovedAt.IsZero() {
		approvedAt = &store.Review.ApprovedAt
	}
	return reviewSummary{Status: store.ReviewStatus(snap.Hash), DiffHash: snap.Hash, ApprovedDiffHash: store.Review.ApprovedDiffHash, ApprovedAt: approvedAt, OpenThreads: open, CurrentThreads: current, StaleThreads: stale, TotalThreads: len(store.Threads)}
}

func buildReviewContext(store *thread.Store, snap snapshot.Snapshot) reviewContext {
	files := make([]fileSummary, 0, len(snap.Files))
	for _, f := range snap.Files {
		files = append(files, fileSummary{Path: f.Path(), HunkCount: len(f.Hunks)})
	}
	return reviewContext{Review: summarizeReview(store, snap), GitHub: store.GitHub, Viewed: store.Viewed, Files: files, Threads: threadViews(store.Threads, snap.Files)}
}

func threadViews(threads []thread.Thread, files []diff.File) []threadView {
	out := make([]threadView, 0, len(threads))
	for _, t := range threads {
		out = append(out, threadView{Thread: t, Freshness: freshness(t, files), LastActor: thread.LastActor(t)})
	}
	return out
}

func freshness(t thread.Thread, files []diff.File) string {
	if isThreadCurrent(t, files) {
		return "current"
	}
	return "stale"
}

func isThreadCurrent(t thread.Thread, files []diff.File) bool {
	for _, f := range files {
		if f.Path() != t.Path {
			continue
		}
		for _, h := range f.Hunks {
			for _, l := range h.Lines {
				lineNo := l.NewNo
				if t.Side == thread.SideOld {
					lineNo = l.OldNo
				}
				if lineNo >= t.LineStart && lineNo <= t.LineEnd {
					return true
				}
			}
		}
	}
	return false
}

type cliTarget struct {
	hunkHeader string
	context    string
}

func targetFromSnapshot(files []diff.File, path string, side thread.Side, start, end int) (cliTarget, error) {
	for _, f := range files {
		if f.Path() != path {
			continue
		}
		for _, h := range f.Hunks {
			var lines []string
			for _, l := range h.Lines {
				lineNo := l.NewNo
				if side == thread.SideOld {
					lineNo = l.OldNo
				}
				if lineNo >= start && lineNo <= end {
					lines = append(lines, l.Text)
				}
			}
			if len(lines) > 0 {
				return cliTarget{hunkHeader: h.Header, context: strings.Join(lines, "\n")}, nil
			}
		}
	}
	return cliTarget{}, fmt.Errorf("target not found in current diff: %s:%d-%d (%s)", path, start, end, side)
}

func parseSide(s string) (thread.Side, error) {
	switch strings.ToLower(s) {
	case "new", "right":
		return thread.SideNew, nil
	case "old", "left":
		return thread.SideOld, nil
	default:
		return "", fmt.Errorf("invalid side %q", s)
	}
}

func parseActor(s string) (thread.Actor, error) {
	switch strings.ToLower(s) {
	case "human":
		return thread.ActorHuman, nil
	case "agent":
		return thread.ActorAgent, nil
	case "github":
		return thread.ActorGitHub, nil
	case "system":
		return thread.ActorSystem, nil
	default:
		return "", fmt.Errorf("invalid actor %q", s)
	}
}

func readBody(s string) (string, error) {
	if s == "-" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(b)), nil
	}
	return strings.TrimSpace(s), nil
}

func writeJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func streamEvents(path string, follow bool, jsonLines bool) error {
	var offset int64
	if err := readEventsFrom(path, 0, func(line []byte) error {
		fmt.Println(string(line))
		return nil
	}, &offset); err != nil {
		return err
	}
	if !follow {
		return nil
	}
	for {
		time.Sleep(500 * time.Millisecond)
		if err := readEventsFrom(path, offset, func(line []byte) error {
			fmt.Println(string(line))
			return nil
		}, &offset); err != nil {
			return err
		}
		_ = jsonLines
	}
}

func readEventsFrom(path string, offset int64, emit func([]byte) error, newOffset *int64) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			*newOffset = 0
			return nil
		}
		return err
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		if err := emit(line); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	pos, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	*newOffset = pos
	return nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
