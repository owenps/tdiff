package snapshot

import "testing"

func TestFromRawParsesFilesAndHashesRawDiff(t *testing.T) {
	raw := `diff --git a/foo.go b/foo.go
index 1111111..2222222 100644
--- a/foo.go
+++ b/foo.go
@@ -1 +1 @@
-old
+new
`
	s, err := FromRaw(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Files) != 1 || s.Files[0].Path() != "foo.go" {
		t.Fatalf("files=%+v", s.Files)
	}
	if s.Hash != "09b87c20ab340db896ad571a7fd71203724162f6" {
		t.Fatalf("hash=%q", s.Hash)
	}
}

func TestFromRawAddsParseContext(t *testing.T) {
	_, err := FromRaw("diff --git a/foo.go b/foo.go\n@@ nope\n")
	if err == nil || err.Error() == "parse hunk header \"@@ nope\"" {
		t.Fatalf("expected contextual parse error, got %v", err)
	}
}
