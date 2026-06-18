package diff

import "testing"

func TestParseUnifiedDiff(t *testing.T) {
	raw := `diff --git a/foo.go b/foo.go
index 1111111..2222222 100644
--- a/foo.go
+++ b/foo.go
@@ -1,3 +1,3 @@
 package foo
-old
+new
`
	files, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("files=%d", len(files))
	}
	if files[0].Path() != "foo.go" {
		t.Fatalf("path=%q", files[0].Path())
	}
	lines := files[0].Hunks[0].Lines
	if lines[1].Kind != Delete || lines[1].OldNo != 2 || lines[1].NewNo != 0 {
		t.Fatalf("delete line=%+v", lines[1])
	}
	if lines[2].Kind != Add || lines[2].OldNo != 0 || lines[2].NewNo != 2 {
		t.Fatalf("add line=%+v", lines[2])
	}
}
