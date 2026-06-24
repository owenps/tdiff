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
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/owenps/tdiff/internal/app"
	"github.com/owenps/tdiff/internal/diff"
	"github.com/owenps/tdiff/internal/git"
	gh "github.com/owenps/tdiff/internal/github"
	"github.com/owenps/tdiff/internal/snapshot"
	"github.com/owenps/tdiff/internal/thread"
	"github.com/owenps/tdiff/internal/threadtarget"
)

type diffFlags struct {
	base             string
	mode             git.Mode
	ignoreWhitespace bool
}

type loadedReview struct {
	repoRoot string
	store    *thread.Store
	snap     snapshot.Snapshot
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

type agentInbox struct {
	Review  reviewSummary    `json:"review"`
	Threads []agentInboxItem `json:"threads"`
}

type agentInboxItem struct {
	Thread      threadView `json:"thread"`
	BodyPreview string     `json:"body_preview"`
}

type fileSummary struct {
	Path      string `json:"path"`
	HunkCount int    `json:"hunk_count"`
}

func Run() error {
	ctx := context.Background()
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "help", "--help", "-h":
			fmt.Print(mainHelpText())
			return nil
		case "agent":
			return runAgent(os.Args[2:])
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

func runAgent(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Print(agentHelpText())
		return nil
	}
	if args[0] == "inbox" {
		return runAgentInbox(context.Background(), args[1:])
	}
	return fmt.Errorf("unknown agent command %q", args[0])
}

func mainHelpText() string {
	return `tdiff - terminal diff review

Usage:
  tdiff [--base <ref>] [--staged|--unstaged] [--offline] [--debug]
  tdiff review status|context|approve|unapprove|watch|events
  tdiff thread list|show|add|reply|resolve|reopen
  tdiff agent help|inbox [limit]

Agent quick start:
  tdiff agent inbox     # current open review threads for agents
  tdiff review watch    # compact text event stream; add --json for JSONL

Common commands:
  tdiff agent inbox 5 --json
  tdiff review context --json
  tdiff thread show <id> --json
  tdiff thread reply <id> --actor agent --body "Fixed; added test"

`
}

func agentHelpText() string {
	return `tdiff agent help

Purpose:
  Human reviews your code in tdiff. You watch events, inspect threads on demand,
  fix code, and reply in the relevant thread.

Workflow:
  1. Ask human to run: tdiff
  2. Read current work: tdiff agent inbox --json
  3. Watch notifications if waiting: tdiff review watch
  4. Inspect detail if needed:
       tdiff thread show <thread_id> --json
       tdiff review context --json
  5. Fix code, run relevant checks.
  6. Reply in the same thread:
       tdiff thread reply <thread_id> --actor agent --body "Fixed; ran go test ./..."
  7. Resolve only when clearly handled:
       tdiff thread resolve <thread_id> --actor agent
  8. Stop when review.approved appears for the current diff.

Event stream:
  tdiff review watch emits compact text with explicit keys and polls attached GitHub PRs:
    E1 thread.created thread_id=T1 actor=human path="internal/foo.go" line_start=3 body_preview="Handle nil repo"

Rules:
  - Treat tdiff agent inbox as the work queue.
  - Treat events as notifications; use thread/context JSON for source of truth.
  - Do not ask user to copy/paste comments; read tdiff directly.
  - Do not approve unless explicitly asked by user.
  - Always reply in-thread after acting, with brief evidence.

JSON options:
  tdiff review watch --json
  tdiff review events --json

`
}

func runAgentInbox(ctx context.Context, args []string) error {
	positionalLimit, args, err := extractAgentInboxLimit(args)
	if err != nil {
		return err
	}
	dfs := newDiffFlagSet("tdiff agent inbox")
	limitFlag := dfs.fs.Int("limit", 0, "max threads to return")
	jsonOut := dfs.fs.Bool("json", false, "emit JSON")
	if err := dfs.fs.Parse(args); err != nil {
		return err
	}
	if dfs.fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", dfs.fs.Arg(0))
	}
	limit := *limitFlag
	if positionalLimit > 0 {
		limit = positionalLimit
	}
	loaded, err := loadReview(ctx, dfs.opts())
	if err != nil {
		return err
	}
	index := buildAgentInbox(loaded.store, loaded.snap, limit)
	if *jsonOut {
		return writeJSON(index)
	}
	for _, item := range index.Threads {
		t := item.Thread
		fmt.Printf("%s %s:%d actor=%s replies=%d %s\n", t.ID, t.Path, t.LineStart, t.LastActor, threadReplyCount(t.Thread), item.BodyPreview)
	}
	return nil
}

func extractAgentInboxLimit(args []string) (int, []string, error) {
	limit := 0
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			out = append(out, arg)
			if (arg == "--base" || arg == "-base" || arg == "--limit" || arg == "-limit") && i+1 < len(args) {
				i++
				out = append(out, args[i])
			}
			continue
		}
		parsed, err := strconv.Atoi(arg)
		if err != nil {
			out = append(out, arg)
			continue
		}
		if parsed < 0 {
			return 0, nil, fmt.Errorf("invalid limit %q", arg)
		}
		if limit != 0 {
			return 0, nil, fmt.Errorf("multiple limits provided")
		}
		limit = parsed
	}
	return limit, out, nil
}

func runTUI(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("tdiff", flag.ExitOnError)
	base := fs.String("base", "", "base branch/ref for branch diff")
	staged := fs.Bool("staged", false, "show staged diff")
	unstaged := fs.Bool("unstaged", false, "show unstaged diff")
	ignoreWhitespace := fs.Bool("ignore-space-change", false, "ignore whitespace changes")
	offline := fs.Bool("offline", false, "disable network/external integrations")
	debug := fs.Bool("debug", false, "write debug log to .git/tdiff/debug.log")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !isInteractiveTerminal() {
		return errors.New(nonInteractiveTUIHelp())
	}
	mode := modeFromFlags(*staged, *unstaged)
	m, err := app.New(ctx, app.Config{Base: *base, Mode: mode, IgnoreWhitespace: *ignoreWhitespace, Offline: *offline, Debug: *debug})
	if err != nil {
		return err
	}
	_, err = tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

func isInteractiveTerminal() bool {
	return isTerminalFile(os.Stdin) && isTerminalFile(os.Stdout)
}

func isTerminalFile(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func nonInteractiveTUIHelp() string {
	return "tdiff opens an interactive TUI; this shell is not interactive.\nFor agent/CLI workflows: tdiff agent --help\nFor all commands: tdiff --help"
}

func runReview(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: tdiff review status|context|approve|unapprove|watch|events")
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
		return runReviewEvents(ctx, "tdiff review watch", args[1:], true)
	case "events":
		return runReviewEvents(ctx, "tdiff review events", args[1:], false)
	default:
		return fmt.Errorf("unknown review command %q", sub)
	}
}

func runReviewEvents(ctx context.Context, name string, args []string, followDefault bool) error {
	dfs := newDiffFlagSet(name)
	jsonOut := dfs.fs.Bool("json", false, "emit JSON lines")
	follow := dfs.fs.Bool("follow", followDefault, "follow new events")
	pollGitHub := dfs.fs.Bool("poll-github", followDefault, "poll attached GitHub PR while following")
	pollInterval := dfs.fs.Duration("poll-interval", 30*time.Second, "GitHub polling interval")
	if err := dfs.fs.Parse(args); err != nil {
		return err
	}
	loaded, err := loadReview(ctx, dfs.opts())
	if err != nil {
		return err
	}
	opts := eventStreamOptions{follow: *follow, jsonLines: *jsonOut}
	if *follow && *pollGitHub && loaded.store.GitHub != nil {
		repoRoot := loaded.repoRoot
		opts.pollInterval = *pollInterval
		opts.poll = func() error { return syncAttachedGitHub(ctx, repoRoot) }
	}
	return streamEvents(loaded.store.EventsPath(), opts)
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
	repo, err := git.Open(ctx)
	if err != nil {
		return err
	}
	store, err := thread.Open(repo.Root)
	if err != nil {
		return err
	}
	return streamEvents(store.EventsPath(), eventStreamOptions{follow: *follow, jsonLines: *jsonOut})
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
	return loadedReview{repoRoot: repo.Root, store: store, snap: snap}, nil
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

func buildAgentInbox(store *thread.Store, snap snapshot.Snapshot, limit int) agentInbox {
	items := make([]agentInboxItem, 0, len(store.Threads))
	for _, t := range store.Threads {
		if t.Status == thread.StatusResolved || !isThreadCurrent(t, snap.Files) {
			continue
		}
		view := threadView{Thread: t, Freshness: freshness(t, snap.Files), LastActor: thread.LastActor(t)}
		items = append(items, agentInboxItem{Thread: view, BodyPreview: eventBodyPreview(thread.Body(t))})
	}
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i].Thread, items[j].Thread
		if agentActorRank(left.LastActor) != agentActorRank(right.LastActor) {
			return agentActorRank(left.LastActor) < agentActorRank(right.LastActor)
		}
		return left.UpdatedAt.After(right.UpdatedAt)
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return agentInbox{Review: summarizeReview(store, snap), Threads: items}
}

func agentActorRank(actor thread.Actor) int {
	switch actor {
	case thread.ActorHuman, thread.ActorGitHub:
		return 0
	case thread.ActorAgent:
		return 2
	default:
		return 1
	}
}

func threadReplyCount(t thread.Thread) int {
	return max(0, len(t.Messages)-1)
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
	return threadtarget.CurrentInFiles(t, files)
}

type cliTarget struct {
	hunkHeader string
	context    string
}

func targetFromSnapshot(files []diff.File, path string, side thread.Side, start, end int) (cliTarget, error) {
	hunkHeader, context, ok := threadtarget.ContextForRange(files, path, side, start, end)
	if !ok {
		return cliTarget{}, fmt.Errorf("target not found in current diff: %s:%d-%d (%s)", path, start, end, side)
	}
	return cliTarget{hunkHeader: hunkHeader, context: context}, nil
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

type eventStreamOptions struct {
	follow       bool
	jsonLines    bool
	poll         func() error
	pollInterval time.Duration
}

func streamEvents(path string, opts eventStreamOptions) error {
	emit := func(line []byte) error {
		if opts.jsonLines {
			fmt.Println(string(line))
			return nil
		}
		text, err := eventTextLine(line)
		if err != nil {
			return err
		}
		fmt.Println(text)
		return nil
	}

	var offset int64
	if err := readEventsFrom(path, 0, emit, &offset); err != nil {
		return err
	}
	if !opts.follow {
		return nil
	}
	if opts.pollInterval <= 0 {
		opts.pollInterval = 30 * time.Second
	}
	nextPoll := time.Now()
	for {
		now := time.Now()
		if opts.poll != nil && !now.Before(nextPoll) {
			if err := opts.poll(); err != nil {
				fmt.Fprintf(os.Stderr, "github poll failed: %v\n", err)
			}
			nextPoll = now.Add(opts.pollInterval)
		}
		if err := readEventsFrom(path, offset, emit, &offset); err != nil {
			return err
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func syncAttachedGitHub(ctx context.Context, repoRoot string) error {
	store, err := thread.Open(repoRoot)
	if err != nil {
		return err
	}
	if store.GitHub == nil {
		return nil
	}
	client := gh.NewClient(repoRoot)
	threads, err := client.Threads(ctx, *store.GitHub)
	if err != nil {
		return err
	}
	_, err = store.SyncGitHubThreads(*store.GitHub, threads)
	return err
}

func eventTextLine(line []byte) (string, error) {
	var e thread.Event
	if err := json.Unmarshal(line, &e); err != nil {
		return "", err
	}
	parts := []string{e.ID, e.Type}
	if e.ThreadID != "" {
		parts = append(parts, "thread_id="+e.ThreadID)
	}
	if e.Source != "" {
		parts = append(parts, "source="+string(e.Source))
	}
	if e.Actor != "" {
		parts = append(parts, "actor="+string(e.Actor))
	}
	if e.Status != "" {
		parts = append(parts, "status="+string(e.Status))
	}
	if e.Path != "" {
		parts = append(parts, "path="+strconv.Quote(e.Path))
	}
	if e.LineStart != 0 {
		parts = append(parts, fmt.Sprintf("line_start=%d", e.LineStart))
	}
	if e.LineEnd != 0 && e.LineEnd != e.LineStart {
		parts = append(parts, fmt.Sprintf("line_end=%d", e.LineEnd))
	}
	if e.Side != "" {
		parts = append(parts, "side="+string(e.Side))
	}
	if e.DiffHash != "" {
		parts = append(parts, "diff_hash="+e.DiffHash)
	}
	if body := eventBodyPreview(e.Body); body != "" {
		parts = append(parts, "body_preview="+strconv.Quote(body))
	}
	return strings.Join(parts, " "), nil
}

func eventBodyPreview(body string) string {
	body = firstLine(body)
	const max = 160
	runes := []rune(body)
	if len(runes) <= max {
		return body
	}
	return string(runes[:max]) + "…"
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
