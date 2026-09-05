package keymap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// press builds the key press a terminal delivers for a keystroke. The editor
// captures msg.String(), exactly as key.Matches does, so the round-trip
// assertion is what keeps these tests on the real path.
func press(t *testing.T, keystroke string) tea.KeyPressMsg {
	t.Helper()
	msg := tea.KeyPressMsg{}
	code := keystroke
	for {
		modifier, rest, found := strings.Cut(code, "+")
		if !found || rest == "" {
			break
		}
		switch modifier {
		case "ctrl":
			msg.Mod |= tea.ModCtrl
		case "alt":
			msg.Mod |= tea.ModAlt
		case "shift":
			msg.Mod |= tea.ModShift
		default:
			t.Fatalf("unsupported modifier %q in %q", modifier, keystroke)
		}
		code = rest
	}
	switch code {
	case "up":
		msg.Code = tea.KeyUp
	case "down":
		msg.Code = tea.KeyDown
	case "enter":
		msg.Code = tea.KeyEnter
	case "esc":
		msg.Code = tea.KeyEscape
	default:
		msg.Code = rune(code[0])
		if msg.Mod == 0 {
			// An unmodified printable key arrives carrying its text.
			msg.Text = code
		}
	}
	if msg.String() != keystroke {
		t.Fatalf("constructed press for %q renders as %q", keystroke, msg.String())
	}
	return msg
}

// editorFor loads a keymap into a temporary file and opens an editor on it,
// with the cursor moved onto id.
func editorFor(t *testing.T, id string) (*Editor, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".sgpt-keymap.json")
	if err := Load(path); err != nil {
		t.Fatal(err)
	}
	editor := NewEditor()
	editor.SetSize(120, 40)
	for index, b := range editor.bindings {
		if b.ID == id {
			editor.cursor = index
			return editor, path
		}
	}
	t.Fatalf("binding %q is not in the editor", id)
	return nil, ""
}

func TestEditorRebindsAndPersists(t *testing.T) {
	binding := New("editor.submit", "Send message", "ctrl+j")
	editor, path := editorFor(t, "editor.submit")

	if done := editor.HandleKey(press(t, "enter")); done {
		t.Fatal("enter closed the editor, want it to start capturing")
	}
	if !editor.capturing {
		t.Fatal("enter did not start capturing")
	}
	if done := editor.HandleKey(press(t, "z")); done {
		t.Fatal("capturing a key closed the editor")
	}

	if got := binding.KeysString(); got != "z" {
		t.Errorf("keys = %q, want %q", got, "z")
	}
	if editor.capturing {
		t.Error("still capturing after a key was captured")
	}
	// The rebinding must reach the file, not just the running registry.
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	config := &configuration{}
	if err := json.Unmarshal(content, config); err != nil {
		t.Fatal(err)
	}
	for _, b := range config.Bindings {
		if b.ID != "editor.submit" {
			continue
		}
		if len(b.Keys) != 1 || b.Keys[0] != "z" {
			t.Errorf("persisted keys = %v, want [z]", b.Keys)
		}
		return
	}
	t.Error("rebound binding is missing from the written file")
}

func TestEditorRejectsConflictWithoutRebinding(t *testing.T) {
	binding := New("conflict.submit", "Send message", "ctrl+j")
	New("conflict.info", "Show info", "alt+i")
	editor, _ := editorFor(t, "conflict.submit")

	editor.HandleKey(press(t, "enter"))
	editor.HandleKey(press(t, "alt+i"))

	if got := binding.KeysString(); got != "ctrl+j" {
		t.Errorf("keys = %q after a conflicting capture, want the original %q", got, "ctrl+j")
	}
	if !editor.failed {
		t.Error("a conflicting capture was not reported as a failure")
	}
	if !strings.Contains(editor.status, "conflict.info") {
		t.Errorf("status = %q, want it to name the conflicting binding", editor.status)
	}
}

func TestEditorEscapeCancelsCaptureThenCloses(t *testing.T) {
	binding := New("escape.submit", "Send message", "ctrl+j")
	editor, _ := editorFor(t, "escape.submit")

	editor.HandleKey(press(t, "enter"))
	if done := editor.HandleKey(press(t, "esc")); done {
		t.Fatal("esc closed the editor while capturing, want it to cancel the capture")
	}
	if got := binding.KeysString(); got != "ctrl+j" {
		t.Errorf("keys = %q after a cancelled capture, want %q", got, "ctrl+j")
	}
	if !editor.HandleKey(press(t, "esc")) {
		t.Error("esc did not close the editor once browsing")
	}
}

func TestEditorAcceptsKeyUsedInAnotherScope(t *testing.T) {
	binding := New("scopea.submit", "Send message", "ctrl+j")
	New("scopeb.quit", "Quit", "alt+u")
	editor, _ := editorFor(t, "scopea.submit")

	editor.HandleKey(press(t, "enter"))
	editor.HandleKey(press(t, "alt+u"))

	if got := binding.KeysString(); got != "alt+u" {
		t.Errorf("keys = %q, want %q: the same key in another scope is allowed", got, "alt+u")
	}
	if editor.failed {
		t.Errorf("rebinding was reported as a failure: %s", editor.status)
	}
}

func TestEditorNavigationStaysInBounds(t *testing.T) {
	New("nav.first", "First", "f1")
	editor, _ := editorFor(t, "nav.first")

	editor.cursor = 0
	editor.HandleKey(press(t, "up"))
	if editor.cursor != 0 {
		t.Errorf("cursor = %d after up at the top, want 0", editor.cursor)
	}
	editor.cursor = len(editor.bindings) - 1
	editor.HandleKey(press(t, "down"))
	if editor.cursor != len(editor.bindings)-1 {
		t.Errorf("cursor = %d after down at the bottom, want %d", editor.cursor, len(editor.bindings)-1)
	}
	editor.cursor = 0
	editor.HandleKey(press(t, "down"))
	if editor.cursor != 1 {
		t.Errorf("cursor = %d after down, want 1", editor.cursor)
	}
}

func TestEditorViewShowsEveryBindingAcrossScroll(t *testing.T) {
	New("view.only", "The only one", "alt+q")
	editor, _ := editorFor(t, "view.only")
	editor.SetSize(120, 40)

	// Walk the whole list; every binding must appear while it is selected,
	// so nothing is unreachable behind the scroll window.
	for index, b := range editor.bindings {
		editor.cursor = index
		if !strings.Contains(editor.View(), b.ID) {
			t.Fatalf("binding %q is not visible when selected", b.ID)
		}
	}
}
