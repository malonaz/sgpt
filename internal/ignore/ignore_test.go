package ignore

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, root, path, content string) {
	t.Helper()
	full := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMatcher(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".gitignore", "*.log\nbuild/\n/rooted\n!keep.log\n")
	write(t, root, "sub/.gitignore", "secret.txt\n")

	m := NewMatcher(root, []string{"plz-out", "**/generated"})
	m.LoadDirectory("sub")

	cases := []struct {
		path        string
		isDirectory bool
		want        bool
	}{
		{"a.log", false, true},
		{"deep/nested/b.log", false, true},
		{"keep.log", false, false},
		{"build", true, true},
		{"build/artifact", false, true},
		{"build", false, false}, // directory-only pattern
		{"rooted", false, true},
		{"sub/rooted", false, false}, // anchored to root
		{"sub/secret.txt", false, true},
		{"secret.txt", false, false}, // anchored to sub/
		{"plz-out", true, true},
		{"x/y/generated", true, true},
		{"x/y/generated/file.go", false, true},
		{"src/main.go", false, false},
	}
	for _, c := range cases {
		if got := m.Ignored(c.path, c.isDirectory); got != c.want {
			t.Errorf("Ignored(%q, dir=%t) = %t, want %t", c.path, c.isDirectory, got, c.want)
		}
	}
}
