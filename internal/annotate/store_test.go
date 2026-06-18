package annotate

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
	want := filepath.Join(root, ".git", "tdiff", "annotations.json")
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
	want := filepath.Join(gitDir, "tdiff", "annotations.json")
	if store.path != want {
		t.Fatalf("path = %q, want %q", store.path, want)
	}
}

func TestOpenMigratesLegacyNotesFile(t *testing.T) {
	root := t.TempDir()
	gitDir := filepath.Join(root, ".git")
	storeDir := filepath.Join(gitDir, "tdiff")
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{"notes":[{"id":"a1","path":"foo.go","side":"new","line_start":3,"line_end":3}],"viewed":[{"path":"foo.go","diff_hash":"h"}]}`
	if err := os.WriteFile(filepath.Join(storeDir, "notes.json"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(store.Annotations); got != 1 {
		t.Fatalf("annotations = %d, want 1", got)
	}
	wantPath := filepath.Join(storeDir, "annotations.json")
	if store.path != wantPath {
		t.Fatalf("path = %q, want %q", store.path, wantPath)
	}
}
