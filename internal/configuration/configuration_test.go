package configuration

import (
	"os"
	"path/filepath"
	"testing"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFindOverrideConfigPathsPrecedence(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b")
	write(t, filepath.Join(root, overrideFileName), "{}")
	write(t, filepath.Join(root, localOverrideFileName), "{}")
	write(t, filepath.Join(nested, overrideFileName), "{}")

	t.Chdir(nested)
	paths, err := findOverrideConfigPaths()
	if err != nil {
		t.Fatal(err)
	}

	// Highest precedence first: cwd-most before root-most, and the local
	// override before the committed file beside it.
	want := []string{
		filepath.Join(nested, overrideFileName),
		filepath.Join(root, localOverrideFileName),
		filepath.Join(root, overrideFileName),
	}
	if len(paths) < len(want) {
		t.Fatalf("got %v, want it to start with %v", paths, want)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("got %v, want it to start with %v", paths, want)
		}
	}
}

// A local override outranks the committed file in the same directory, which is
// the whole point: personal settings stay out of the repo.
func TestLocalOverrideWins(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, overrideFileName), `{"chat": {"user": "organizations/o/users/committed"}}`)
	write(t, filepath.Join(root, localOverrideFileName), `{"chat": {"user": "organizations/o/users/local"}}`)

	t.Chdir(root)
	paths, err := findOverrideConfigPaths()
	if err != nil {
		t.Fatal(err)
	}

	configuration := &sgptpb.Configuration{}
	if err := mergeOverrides(configuration, paths); err != nil {
		t.Fatal(err)
	}
	if got := configuration.GetChat().GetUser(); got != "organizations/o/users/local" {
		t.Fatalf("chat.user = %q, want the local override to win", got)
	}
}

func TestLoadIgnoreUnionsBothFiles(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, overrideFileName), `{"ignore": ["plz-out/"]}`)
	write(t, filepath.Join(root, localOverrideFileName), `{"ignore": ["scratch/"]}`)

	got := LoadIgnore(root)
	want := map[string]bool{"plz-out/": true, "scratch/": true}
	if len(got) != len(want) {
		t.Fatalf("got %v, want the union of both files", got)
	}
	for _, pattern := range got {
		if !want[pattern] {
			t.Fatalf("got %v, want the union of both files", got)
		}
	}
}
