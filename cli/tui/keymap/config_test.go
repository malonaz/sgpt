package keymap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Each test declares its own bindings on keys nothing else claims: the
// registry is process-global, and Load validates and rewrites all of it.
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
	binding := New("defaults_submit", "Send message", "f1")
	// A directory that does not exist yet: first run has no ~/.config/sgpt.
	path := filepath.Join(t.TempDir(), "nested", ".sgpt-keymap.json")

	if err := Load(path); err != nil {
		t.Fatal(err)
	}
	if got := binding.KeysString(); got != "f1" {
		t.Errorf("binding keys = %q, want %q", got, "f1")
	}
	idToKeys := readKeymap(t, path)
	if got := idToKeys["defaults_submit"]; len(got) != 1 || got[0] != "f1" {
		t.Errorf("written keys = %v, want [f1]", got)
	}
	if len(idToKeys) != len(registry) {
		t.Errorf("wrote %d bindings, want all %d", len(idToKeys), len(registry))
	}
}

func TestLoadOverridesAndUnbinds(t *testing.T) {
	rebound := New("override_submit", "Send message", "f2")
	unbound := New("override_quit", "Quit", "f3")
	multi := New("override_help", "Help", "f4")
	path := filepath.Join(t.TempDir(), ".sgpt-keymap.json")
	writeKeymap(t, path, `{"bindings": [
	  {"id": "override_submit", "keys": ["f21"]},
	  {"id": "override_quit", "keys": []},
	  {"id": "override_help", "keys": ["f4", "f20"]}
	]}`)

	if err := Load(path); err != nil {
		t.Fatal(err)
	}
	if got := rebound.KeysString(); got != "f21" {
		t.Errorf("rebound keys = %q, want %q", got, "f21")
	}
	if got := unbound.KeysString(); got != "" {
		t.Errorf("unbound keys = %q, want empty", got)
	}
	if got := multi.KeysString(); got != "f4 / f20" {
		t.Errorf("multi keys = %q, want %q", got, "f4 / f20")
	}
}

func TestLoadKeepsDefaultsForOmittedBindingsAndRewrites(t *testing.T) {
	listed := New("omitted_submit", "Send message", "f5")
	// Declared after the user's file was written: must survive on its default
	// and be added back to the file.
	added := New("omitted_info", "Show info", "f6")
	path := filepath.Join(t.TempDir(), ".sgpt-keymap.json")
	writeKeymap(t, path, `{"bindings": [{"id": "omitted_submit", "keys": ["f22"]}]}`)

	if err := Load(path); err != nil {
		t.Fatal(err)
	}
	if got := listed.KeysString(); got != "f22" {
		t.Errorf("listed keys = %q, want %q", got, "f22")
	}
	if got := added.KeysString(); got != "f6" {
		t.Errorf("added keys = %q, want %q", got, "f6")
	}
	idToKeys := readKeymap(t, path)
	if got := idToKeys["omitted_submit"]; len(got) != 1 || got[0] != "f22" {
		t.Errorf("rewritten override = %v, want [f22]", got)
	}
	if got := idToKeys["omitted_info"]; len(got) != 1 || got[0] != "f6" {
		t.Errorf("rewritten addition = %v, want [f6]", got)
	}
}

func TestLoadRejectsBadFiles(t *testing.T) {
	New("reject_submit", "Send message", "f7")
	New("reject_quit", "Quit", "f8")

	cases := []struct {
		name    string
		content string
		wantErr string
	}{{
		name:    "unknown id",
		content: `{"bindings": [{"id": "reject_nope", "keys": ["f23"]}]}`,
		wantErr: `unknown binding id "reject_nope"`,
	}, {
		name:    "duplicate id",
		content: `{"bindings": [{"id": "reject_submit", "keys": ["f23"]}, {"id": "reject_submit", "keys": ["f24"]}]}`,
		wantErr: `binding id "reject_submit" listed twice`,
	}, {
		name:    "key already claimed",
		content: `{"bindings": [{"id": "reject_submit", "keys": ["f8"]}]}`,
		wantErr: `"f8" is bound to both`,
	}, {
		name:    "key bound twice to one binding",
		content: `{"bindings": [{"id": "reject_submit", "keys": ["f23", "f23"]}]}`,
		wantErr: `"f23" is bound twice to "reject_submit"`,
	}, {
		name:    "unknown field",
		content: `{"bindings": [{"id": "reject_submit", "key": ["f23"]}]}`,
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
	rejected := New("intact_submit", "Send message", "f9")
	New("intact_quit", "Quit", "f10")
	path := filepath.Join(t.TempDir(), ".sgpt-keymap.json")
	// A valid rebinding followed by one that conflicts: neither may land.
	writeKeymap(t, path, `{"bindings": [
	  {"id": "intact_submit", "keys": ["f10"]}
	]}`)

	if err := Load(path); err == nil {
		t.Fatal("Load succeeded on a conflicting file")
	}
	if got := rejected.KeysString(); got != "f9" {
		t.Errorf("keys = %q after a rejected file, want the default %q", got, "f9")
	}
}

func TestWrittenFileIsHandEditable(t *testing.T) {
	New("escaping_to_top", "Jump to top", "alt+shift+<")
	path := filepath.Join(t.TempDir(), ".sgpt-keymap.json")

	if err := Load(path); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `"alt+shift+<"`) {
		t.Errorf("keys were escaped rather than written literally:\n%s", content)
	}
	// Re-reading what was written must be a no-op, never an error.
	if err := Load(path); err != nil {
		t.Fatalf("reloading a generated keymap failed: %v", err)
	}
}

// The check the global model exists for: one key, one action. Two bindings
// on it means the second could never fire, however far apart the two actions
// are on screen.
func TestKeyClaimedTwiceIsRejected(t *testing.T) {
	New("global_one", "One", "f11")
	New("global_two", "Two", "f12")
	path := filepath.Join(t.TempDir(), ".sgpt-keymap.json")
	writeKeymap(t, path, `{"bindings": [{"id": "global_two", "keys": ["f11"]}]}`)

	err := Load(path)
	if err == nil {
		t.Fatal("Load accepted a key claimed by two bindings")
	}
	if !strings.Contains(err.Error(), `"f11" is bound to both`) {
		t.Errorf("error = %v, want it to name the conflict", err)
	}
}

func TestSharedKeysAreAllowed(t *testing.T) {
	// The one declared overlap: ctrl+c quits, and falls through to the chat
	// screen to cancel the turn while one is streaming.
	quit := New("quit", "Quit", "ctrl+c")
	cancel := New("cancel", "Cancel stream", "ctrl+c")
	path := filepath.Join(t.TempDir(), ".sgpt-keymap.json")

	if err := Load(path); err != nil {
		t.Fatalf("declared shared key rejected: %v", err)
	}
	if quit.KeysString() != "ctrl+c" || cancel.KeysString() != "ctrl+c" {
		t.Errorf("keys = %q and %q, want both ctrl+c", quit.KeysString(), cancel.KeysString())
	}
}
