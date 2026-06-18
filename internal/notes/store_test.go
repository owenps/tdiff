package notes

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenUsesGitDirDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, ".git", "tdiff", "notes.json")
	if store.path != want {
		t.Fatalf("path = %q, want %q", store.path, want)
	}
}

func TestOpenUsesWorktreeGitDirFile(t *testing.T) {
	root := t.TempDir()
	gitDir := filepath.Join(t.TempDir(), ".git", "worktrees", "example")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: "+gitDir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(gitDir, "tdiff", "notes.json")
	if store.path != want {
		t.Fatalf("path = %q, want %q", store.path, want)
	}
}
