package history

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	historyFileName = "sgpt_chat_history"
	maxHistorySize  = 1000
)

// History manages input history with persistence
type History struct {
	entries []string
	index   int    // Current position in history (-1 means new input)
	current string // Stores current input when navigating history
	mu      sync.Mutex
	path    string
}

// NewHistory creates a new History instance and loads existing history
func NewHistory() *History {
	h := &History{
		entries: make([]string, 0),
		index:   -1,
		path:    filepath.Join(os.TempDir(), historyFileName),
	}
	h.load()
	return h
}

// load reads history from the persistent file
func (h *History) load() {
	h.mu.Lock()
	defer h.mu.Unlock()

	file, err := os.Open(h.path)
	if err != nil {
		return // File doesn't exist yet, that's fine
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		// Unescape newlines stored in history
		line = strings.ReplaceAll(line, "\\n", "\n")
		line = strings.ReplaceAll(line, "\\\\", "\\")
		if line != "" {
			h.entries = append(h.entries, line)
		}
	}

	// Trim to max size if needed
	if len(h.entries) > maxHistorySize {
		h.entries = h.entries[len(h.entries)-maxHistorySize:]
	}
}

// save writes history to the persistent file
func (h *History) save() {
	h.mu.Lock()
	defer h.mu.Unlock()

	file, err := os.Create(h.path)
	if err != nil {
		return // Silent failure for history persistence
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	for _, entry := range h.entries {
		// Escape newlines for storage
		escaped := strings.ReplaceAll(entry, "\\", "\\\\")
		escaped = strings.ReplaceAll(escaped, "\n", "\\n")
		writer.WriteString(escaped + "\n")
	}
	writer.Flush()
}

// Add adds a new entry to history
func (h *History) Add(entry string) {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return
	}

	h.mu.Lock()
	// Don't add duplicates of the last entry
	if len(h.entries) > 0 && h.entries[len(h.entries)-1] == entry {
		h.index = -1
		h.current = ""
		h.mu.Unlock()
		return
	}

	h.entries = append(h.entries, entry)

	// Trim to max size
	if len(h.entries) > maxHistorySize {
		h.entries = h.entries[len(h.entries)-maxHistorySize:]
	}

	h.index = -1
	h.current = ""
	h.mu.Unlock()

	h.save()
}

// Previous returns the previous entry in history
// currentInput is the current textarea content (saved when first navigating)
func (h *History) Previous(currentInput string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if len(h.entries) == 0 {
		return "", false
	}

	// If we're at the newest position, save current input
	if h.index == -1 {
		h.current = currentInput
		h.index = len(h.entries) - 1
	} else if h.index > 0 {
		h.index--
	} else {
		// Already at oldest entry
		return h.entries[0], false
	}

	return h.entries[h.index], true
}

// Next returns the next entry in history (toward present)
func (h *History) Next() (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.index == -1 {
		// Already at newest position
		return "", false
	}

	h.index++

	if h.index >= len(h.entries) {
		// Return to current input
		h.index = -1
		return h.current, true
	}

	return h.entries[h.index], true
}

// Reset resets the navigation index (call when input is modified)
func (h *History) Reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.index = -1
	h.current = ""
}
