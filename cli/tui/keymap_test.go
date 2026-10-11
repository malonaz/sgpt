package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/malonaz/sgpt/cli/tui/keymap"
)

// Bindings are global, and this package pulls every one the binary declares
// into the registry. A new default that lands on a key another binding
// already claims fails here rather than at the user's next start, where a
// rejected keymap takes the whole TUI down.
func TestDefaultBindingsHaveNoConflicts(t *testing.T) {
	if err := keymap.Load(filepath.Join(t.TempDir(), ".sgpt-keymap.json")); err != nil {
		t.Fatal(err)
	}
}

// The conflict the global check exists for: a rebinding onto a key another
// binding already holds leaves one of the two dead.
func TestRebindingOntoAClaimedKeyIsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".sgpt-keymap.json")
	// tab is cycle_focus.
	content := `{"bindings": [{"id": "help", "keys": ["tab"]}]}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	err := keymap.Load(path)
	if err == nil {
		t.Fatal("Load accepted a binding claiming a key another one holds")
	}
	if !strings.Contains(err.Error(), `"tab" is bound to both "cycle_focus" and "help"`) {
		t.Errorf("error = %v, want it to name both bindings", err)
	}
}
