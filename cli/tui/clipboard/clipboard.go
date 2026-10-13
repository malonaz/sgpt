// Package clipboard copies text to the user's clipboard from the TUI.
package clipboard

import (
	"sync"

	tea "charm.land/bubbletea/v2"
	"golang.design/x/clipboard"
)

// native reports whether the system clipboard is reachable; without a display
// (SSH, a bare console) its writes fail silently.
var native = sync.OnceValue(func() bool { return clipboard.Init() == nil })

// Write copies text natively when a display is available, and always via
// OSC 52 so the copy also reaches a local terminal's clipboard over SSH.
func Write(text string) tea.Cmd {
	if native() {
		clipboard.Write(clipboard.FmtText, []byte(text))
	}
	return tea.SetClipboard(text)
}
