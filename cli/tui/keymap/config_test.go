package keymap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Each test declares its own namespace: the registry is process-global, and
// Load validates and rewrites all of it.
func writeKeymap(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readKeymap(t *testing.T, path string) map[string][]string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	config := &configuration{}
	if err := json.Unmarshal(content, config); err != nil {
		t.Fatal(err)
	}
	idToKeys := map[string][]string{}
	for _, b := range config.Bindings {
		idToKeys[b.ID] = b.Keys
	}
	return idToKeys
}

func TestLoadWritesDefaults(t *testing.T) {
	binding := New("defaults.submit", "Send message", "ctrl+j")
	// A directory that does not exist yet: first run has no ~/.config/sgpt.
	path := filepath.Join(t.TempDir(), "nested", ".sgpt-keymap.json")

	if err := Load(path); err != nil {
		t.Fatal(err)
	}
	if got := binding.KeysString(); got != "ctrl+j" {
		t.Errorf("binding keys = %q, want %q", got, "ctrl+j")
	}
	idToKeys := readKeymap(t, path)
	if got := idToKeys["defaults.submit"]; len(got) != 1 || got[0] != "ctrl+j" {
		t.Errorf("written keys = %v, want [ctrl+j]", got)
	}
	if len(idToKeys) != len(registry) {
		t.Errorf("wrote %d bindings, want all %d", len(idToKeys), len(registry))
	}
}

func TestLoadOverridesAndUnbinds(t *testing.T) {
	rebound := New("override.submit", "Send message", "ctrl+j")
	unbound := New("override.quit", "Quit", "ctrl+c")
	multi := New("override.help", "Help", "alt+h")
	path := filepath.Join(t.TempDir(), ".sgpt-keymap.json")
	writeKeymap(t, path, `{"bindings": [
	  {"id": "override.submit", "keys": ["enter"]},
	  {"id": "override.quit", "keys": []},
	  {"id": "override.help", "keys": ["alt+h", "f1"]}
	]}`)

	if err := Load(path); err != nil {
		t.Fatal(err)
	}
	if got := rebound.KeysString(); got != "enter" {
		t.Errorf("rebound keys = %q, want %q", got, "enter")
	}
	if got := unbound.KeysString(); got != "" {
		t.Errorf("unbound keys = %q, want empty", got)
	}
	if got := multi.KeysString(); got != "alt+h / f1" {
		t.Errorf("multi keys = %q, want %q", got, "alt+h / f1")
	}
}

func TestLoadKeepsDefaultsForOmittedBindingsAndRewrites(t *testing.T) {
	listed := New("omitted.submit", "Send message", "ctrl+j")
	// Declared after the user's file was written: must survive on its default
	// and be added back to the file.
	added := New("omitted.info", "Show info", "alt+i")
	path := filepath.Join(t.TempDir(), ".sgpt-keymap.json")
	writeKeymap(t, path, `{"bindings": [{"id": "omitted.submit", "keys": ["enter"]}]}`)

	if err := Load(path); err != nil {
		t.Fatal(err)
	}
	if got := listed.KeysString(); got != "enter" {
		t.Errorf("listed keys = %q, want %q", got, "enter")
	}
	if got := added.KeysString(); got != "alt+i" {
		t.Errorf("added keys = %q, want %q", got, "alt+i")
	}
	idToKeys := readKeymap(t, path)
	if got := idToKeys["omitted.submit"]; len(got) != 1 || got[0] != "enter" {
		t.Errorf("rewritten override = %v, want [enter]", got)
	}
	if got := idToKeys["omitted.info"]; len(got) != 1 || got[0] != "alt+i" {
		t.Errorf("rewritten addition = %v, want [alt+i]", got)
	}
}

func TestLoadRejectsBadFiles(t *testing.T) {
	New("reject.submit", "Send message", "ctrl+j")
	New("reject.quit", "Quit", "ctrl+c")

	cases := []struct {
		name    string
		content string
		wantErr string
	}{{
		name:    "unknown id",
		content: `{"bindings": [{"id": "reject.nope", "keys": ["enter"]}]}`,
		wantErr: `unknown binding id "reject.nope"`,
	}, {
		name:    "duplicate id",
		content: `{"bindings": [{"id": "reject.submit", "keys": ["enter"]}, {"id": "reject.submit", "keys": ["f1"]}]}`,
		wantErr: `binding id "reject.submit" listed twice`,
	}, {
		name:    "conflict within namespace",
		content: `{"bindings": [{"id": "reject.submit", "keys": ["ctrl+c"]}]}`,
		wantErr: `"ctrl+c" is bound to both`,
	}, {
		name:    "unknown field",
		content: `{"bindings": [{"id": "reject.submit", "key": ["enter"]}]}`,
		wantErr: "unknown field",
	}}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".sgpt-keymap.json")
			writeKeymap(t, path, testCase.content)
			err := Load(path)
			if err == nil {
				t.Fatalf("Load succeeded, want error containing %q", testCase.wantErr)
			}
			if !strings.Contains(err.Error(), testCase.wantErr) {
				t.Errorf("error = %v, want it to contain %q", err, testCase.wantErr)
			}
		})
	}
}

func TestRejectedFileLeavesDefaultsIntact(t *testing.T) {
	rejected := New("intact.submit", "Send message", "ctrl+j")
	New("intact.quit", "Quit", "ctrl+c")
	path := filepath.Join(t.TempDir(), ".sgpt-keymap.json")
	// A valid rebinding followed by one that conflicts: neither may land.
	writeKeymap(t, path, `{"bindings": [
	  {"id": "intact.submit", "keys": ["ctrl+c"]}
	]}`)

	if err := Load(path); err == nil {
		t.Fatal("Load succeeded on a conflicting file")
	}
	if got := rejected.KeysString(); got != "ctrl+j" {
		t.Errorf("keys = %q after a rejected file, want the default %q", got, "ctrl+j")
	}
}

func TestWrittenFileIsHandEditable(t *testing.T) {
	New("escaping.to_top", "Jump to top", "alt+<")
	path := filepath.Join(t.TempDir(), ".sgpt-keymap.json")

	if err := Load(path); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `"alt+<"`) {
		t.Errorf("keys were escaped rather than written literally:\n%s", content)
	}
	// Re-reading what was written must be a no-op, never an error.
	if err := Load(path); err != nil {
		t.Fatalf("reloading a generated keymap failed: %v", err)
	}
}

func TestSameKeyAcrossNamespaces(t *testing.T) {
	New("scopeone.cancel", "Cancel", "ctrl+c")
	New("scopetwo.quit", "Quit", "ctrl+c")
	path := filepath.Join(t.TempDir(), ".sgpt-keymap.json")

	if err := Load(path); err != nil {
		t.Fatalf("same key in two scopes rejected: %v", err)
	}
}
