package snapshot

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"

	"github.com/owenps/tdiff/internal/diff"
	"github.com/owenps/tdiff/internal/git"
)

type Snapshot struct {
	Files []diff.File
	Hash  string
}

func Load(ctx context.Context, repo git.Repo, opts git.DiffOptions) (Snapshot, error) {
	raw, err := repo.Diff(ctx, opts)
	if err != nil {
		return Snapshot{}, err
	}
	return FromRaw(raw)
}

func FromRaw(raw string) (Snapshot, error) {
	files, err := diff.Parse(raw)
	if err != nil {
		return Snapshot{}, fmt.Errorf("parse diff snapshot: %w", err)
	}
	return Snapshot{Files: files, Hash: hash(raw)}, nil
}

func hash(s string) string {
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}
