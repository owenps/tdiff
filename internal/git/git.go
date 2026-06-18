package git

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

type Mode string

const (
	ModeBranch   Mode = "branch"
	ModeStaged   Mode = "staged"
	ModeUnstaged Mode = "unstaged"
)

type DiffOptions struct {
	Mode             Mode
	Base             string
	IgnoreWhitespace bool
}

type Repo struct {
	Root string
}

func Open(ctx context.Context) (Repo, error) {
	out, err := run(ctx, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return Repo{}, err
	}
	return Repo{Root: strings.TrimSpace(out)}, nil
}

func (r Repo) Diff(ctx context.Context, opts DiffOptions) (string, error) {
	out := ""
	args := []string{"-C", r.Root, "diff", "--find-renames"}
	if opts.IgnoreWhitespace {
		args = append(args, "--ignore-space-change")
	}

	switch opts.Mode {
	case ModeStaged:
		args = append(args, "--cached")
		tracked, err := run(ctx, "git", args...)
		if err != nil {
			return "", err
		}
		out = tracked
	case ModeUnstaged:
		tracked, err := run(ctx, "git", args...)
		if err != nil {
			return "", err
		}
		out = tracked
		untracked, err := r.untrackedDiff(ctx)
		if err != nil {
			return "", err
		}
		out += untracked
	default:
		base := opts.Base
		if base == "" {
			var err error
			base, err = r.DefaultBase(ctx)
			if err != nil {
				return "", err
			}
		}
		if base != "" {
			baseRev, err := r.mergeBase(ctx, base, "HEAD")
			if err != nil {
				return "", err
			}
			args = append(args, baseRev)
			tracked, err := run(ctx, "git", args...)
			if err != nil {
				return "", err
			}
			out = tracked
		}
		untracked, err := r.untrackedDiff(ctx)
		if err != nil {
			return "", err
		}
		out += untracked
	}

	return out, nil
}

func (r Repo) DefaultBase(ctx context.Context) (string, error) {
	if !r.hasRev(ctx, "HEAD") {
		return "", nil
	}

	if upstream, err := run(ctx, "git", "-C", r.Root, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}"); err == nil {
		upstream = strings.TrimSpace(upstream)
		if upstream != "" && r.hasRev(ctx, upstream) {
			return upstream, nil
		}
	}

	for _, candidate := range []string{"origin/main", "origin/master", "main", "master"} {
		if r.hasRev(ctx, candidate) {
			return candidate, nil
		}
	}

	return "", nil
}

func (r Repo) hasRev(ctx context.Context, rev string) bool {
	_, err := run(ctx, "git", "-C", r.Root, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	return err == nil
}

func (r Repo) mergeBase(ctx context.Context, a, b string) (string, error) {
	out, err := run(ctx, "git", "-C", r.Root, "merge-base", a, b)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func (r Repo) untrackedDiff(ctx context.Context) (string, error) {
	out, err := run(ctx, "git", "-C", r.Root, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return "", err
	}
	if out == "" {
		return "", nil
	}

	var b strings.Builder
	for _, path := range strings.Split(strings.TrimSuffix(out, "\x00"), "\x00") {
		if path == "" {
			continue
		}
		patch, err := runNoIndexDiff(ctx, r.Root, path)
		if err != nil {
			return "", err
		}
		if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
			b.WriteByte('\n')
		}
		b.WriteString(patch)
	}
	return b.String(), nil
}

func run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func runNoIndexDiff(ctx context.Context, root, path string) (string, error) {
	args := []string{"-C", root, "diff", "--no-index", "--", "/dev/null", path}
	cmd := exec.CommandContext(ctx, "git", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
			return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
		}
	}
	return stdout.String(), nil
}
