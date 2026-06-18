package cli

import (
	"context"
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/owenps/tdiff/internal/annotate"
	"github.com/owenps/tdiff/internal/app"
	"github.com/owenps/tdiff/internal/git"
)

func Run() error {
	ctx := context.Background()
	if len(os.Args) > 1 && os.Args[1] == "export" {
		repo, err := git.Open(ctx)
		if err != nil {
			return err
		}
		store, err := annotate.Open(repo.Root)
		if err != nil {
			return err
		}
		fmt.Print(store.ExportMarkdown())
		return nil
	}

	fs := flag.NewFlagSet("tdiff", flag.ExitOnError)
	base := fs.String("base", "", "base branch/ref for branch diff")
	staged := fs.Bool("staged", false, "show staged diff")
	unstaged := fs.Bool("unstaged", false, "show unstaged diff")
	ignoreWhitespace := fs.Bool("ignore-space-change", false, "ignore whitespace changes")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}

	mode := git.ModeBranch
	if *staged {
		mode = git.ModeStaged
	}
	if *unstaged {
		mode = git.ModeUnstaged
	}

	m, err := app.New(ctx, app.Config{Base: *base, Mode: mode, IgnoreWhitespace: *ignoreWhitespace})
	if err != nil {
		return err
	}
	_, err = tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}
