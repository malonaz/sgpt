package keymap

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/malonaz/sgpt/internal/file"
)

// binding is one entry of the keymap file. Help is written for the reader's
// benefit and ignored on read: the binary owns the help text.
type binding struct {
	ID   string   `json:"id"`
	Help string   `json:"help"`
	Keys []string `json:"keys"`
}

type configuration struct {
	Bindings []binding `json:"bindings"`
}

// configPath is the file Load read, remembered so the editor can persist a
// rebinding without the path being threaded back down through the command,
// the app and the editor for the single place that needs it.
var configPath string

// Load applies the user's keymap file to the declared bindings, writing it
// out with the defaults if it does not exist yet.
//
// Bindings the file omits keep their defaults and are written back into it,
// so a file written by an older sgpt gains newly declared bindings rather
// than hiding them. An empty "keys" list unbinds a binding.
func Load(path string) error {
	path, err := file.ExpandPath(path)
	if err != nil {
		return fmt.Errorf("expanding path: %w", err)
	}

	configPath = path

	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		// The bindings already hold their defaults; only the file is missing.
		return write(path)
	}
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	config := &configuration{}
	decoder := json.NewDecoder(bytes.NewReader(content))
	// Strict: a misspelled field would otherwise be a silently ignored
	// rebinding, leaving the user hunting for a key that never fires.
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(config); err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}
	if err := apply(config.Bindings); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if len(config.Bindings) == len(registry) {
		return nil
	}
	// The file predates bindings declared since it was written: rewrite it so
	// every binding stays discoverable and editable.
	return write(path)
}

// apply rebinds the registry from the file's entries. Bindings the file does
// not mention are left on their defaults. The whole result is resolved and
// validated before anything is rebound, so a rejected file leaves every
// binding exactly as it was.
func apply(bindings []binding) error {
	idToKeys := snapshot()
	seen := make(map[string]struct{}, len(bindings))
	for _, b := range bindings {
		if _, ok := registry[b.ID]; !ok {
			return fmt.Errorf("unknown binding id %q", b.ID)
		}
		if _, ok := seen[b.ID]; ok {
			return fmt.Errorf("binding id %q listed twice", b.ID)
		}
		seen[b.ID] = struct{}{}
		idToKeys[b.ID] = b.Keys
	}
	if err := validate(idToKeys); err != nil {
		return err
	}
	commit(idToKeys)
	return nil
}

// rebind points one binding at keys and persists the result. The candidate is
// validated first, so a rejected rebinding leaves the binding untouched and
// the file unwritten.
func rebind(target *Binding, keys ...string) error {
	idToKeys := snapshot()
	idToKeys[target.ID] = keys
	if err := validate(idToKeys); err != nil {
		return err
	}
	commit(idToKeys)
	return write(configPath)
}

// snapshot is the registry's current bindings as a mutable candidate, so a
// change can be validated in full before any of it is applied.
func snapshot() map[string][]string {
	idToKeys := make(map[string][]string, len(registry))
	for id, b := range registry {
		idToKeys[id] = b.Key.Keys()
	}
	return idToKeys
}

func commit(idToKeys map[string][]string) {
	for id, keys := range idToKeys {
		registry[id].Key.SetKeys(keys...)
	}
}

// validate rejects two bindings of the same namespace claiming the same key:
// within one scope only ever one of them could fire. The same key across
// namespaces is fine, and the defaults rely on it — ctrl+c quits the app and
// cancels a stream, ctrl+p scrolls the timeline and moves up the menu.
func validate(idToKeys map[string][]string) error {
	type claim struct{ namespace, key string }
	claimedBy := map[claim]string{}
	// Ordered, so a file with several conflicts always names the same one.
	for _, b := range Bindings() {
		for _, k := range idToKeys[b.ID] {
			c := claim{namespace: namespace(b.ID), key: k}
			if other, ok := claimedBy[c]; ok {
				return fmt.Errorf("%q is bound to both %q and %q", k, other, b.ID)
			}
			claimedBy[c] = b.ID
		}
	}
	return nil
}

// write serializes every declared binding, so the file the user edits always
// lists the complete set rather than just what they have already changed.
func write(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating folders: %w", err)
	}

	config := &configuration{Bindings: make([]binding, 0, len(registry))}
	for _, b := range Bindings() {
		keys := b.Key.Keys()
		if keys == nil {
			// Unbound: marshal as [] rather than null, so the file stays
			// round-trippable by hand.
			keys = []string{}
		}
		config.Bindings = append(config.Bindings, binding{ID: b.ID, Help: b.Help, Keys: keys})
	}

	content := &bytes.Buffer{}
	encoder := json.NewEncoder(content)
	encoder.SetIndent("", "  ")
	// Keys are typed, not rendered as HTML: escaping would spell "alt+<" as
	// "alt+\u003c" in a file meant to be edited by hand.
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(config); err != nil {
		return fmt.Errorf("marshaling keymap: %w", err)
	}
	if err := os.WriteFile(path, content.Bytes(), 0644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}
