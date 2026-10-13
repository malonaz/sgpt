// Package history persists the chat input history across runs.
package history

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const maxEntries = 1000

// Store is the input history every input in the process shares, so an entry
// sent from one tab is recalled in another. Safe for concurrent use.
type Store struct {
	mu      sync.Mutex
	entries []string
	path    string
}

// Open loads the history from $XDG_STATE_HOME/sgpt/history (default
// ~/.local/state/sgpt/history). Persistence is best effort: a missing or
// unwritable file leaves an in-memory history.
func Open() *Store {
	s := &Store{path: defaultPath()}
	s.load()
	return s
}

func defaultPath() string {
	stateHome := os.Getenv("XDG_STATE_HOME")
	if stateHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		stateHome = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(stateHome, "sgpt", "history")
}

func (s *Store) load() {
	if s.path == "" {
		return
	}
	file, err := os.Open(s.path)
	if err != nil {
		return
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(nil, 1<<20)
	for scanner.Scan() {
		if entry := unescape(scanner.Text()); entry != "" {
			s.entries = append(s.entries, entry)
		}
	}
	s.trim()
}

// save rewrites the file; the caller holds mu.
func (s *Store) save() {
	if s.path == "" || os.MkdirAll(filepath.Dir(s.path), 0o700) != nil {
		return
	}
	file, err := os.OpenFile(s.path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	writer := bufio.NewWriter(file)
	for _, entry := range s.entries {
		writer.WriteString(escape(entry) + "\n")
	}
	writer.Flush()
}

func (s *Store) trim() {
	if len(s.entries) > maxEntries {
		s.entries = s.entries[len(s.entries)-maxEntries:]
	}
}

// Add appends an entry, skipping a repeat of the last one.
func (s *Store) Add(entry string) {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.entries) > 0 && s.entries[len(s.entries)-1] == entry {
		return
	}
	s.entries = append(s.entries, entry)
	s.trim()
	s.save()
}

func (s *Store) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.entries
}

// Cursor navigates a Store on behalf of one input.
type Cursor struct {
	store *Store
	// index is the entry shown, counted from the newest; -1 is the draft.
	index int
	draft string
}

func (s *Store) NewCursor() *Cursor {
	return &Cursor{store: s, index: -1}
}

// Previous steps to an older entry, saving draft when leaving it.
func (c *Cursor) Previous(draft string) (string, bool) {
	entries := c.store.snapshot()
	if c.index+1 >= len(entries) {
		return "", false
	}
	if c.index == -1 {
		c.draft = draft
	}
	c.index++
	return entries[len(entries)-1-c.index], true
}

// Next steps to a newer entry, ending back on the draft.
func (c *Cursor) Next() (string, bool) {
	if c.index == -1 {
		return "", false
	}
	c.index--
	if c.index == -1 {
		return c.draft, true
	}
	entries := c.store.snapshot()
	return entries[len(entries)-1-c.index], true
}

// Add records a sent entry and returns to the draft position.
func (c *Cursor) Add(entry string) {
	c.store.Add(entry)
	c.Reset()
}

// Reset returns the cursor to the draft position.
func (c *Cursor) Reset() {
	c.index = -1
	c.draft = ""
}

func escape(entry string) string {
	return strings.NewReplacer(`\`, `\\`, "\n", `\n`).Replace(entry)
}

// unescape inverts escape in one pass, so `\\n` stays a backslash and an n.
func unescape(line string) string {
	var b strings.Builder
	for i := 0; i < len(line); i++ {
		if line[i] == '\\' && i+1 < len(line) {
			i++
			if line[i] == 'n' {
				b.WriteByte('\n')
			} else {
				b.WriteByte(line[i])
			}
			continue
		}
		b.WriteByte(line[i])
	}
	return b.String()
}
