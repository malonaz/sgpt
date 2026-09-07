package keymap

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/malonaz/sgpt/cli/tui/styles"
)

const (
	editorIDWidth   = 26
	editorKeysWidth = 18
	editorMinRows   = 5
	// Rows the box spends on its title, footer, border and padding.
	editorChrome = 10
)

// Editor is the modal keymap editor. Arrow keys browse the declared
// bindings, enter starts capturing, and the next key pressed becomes the
// binding. Every rebinding is written to the keymap file as it is made, so
// what the editor shows and what the file holds never diverge.
type Editor struct {
	bindings []*Binding

	cursor    int
	capturing bool
	// status reports the outcome of the last rebinding, cleared on the next
	// move so it always refers to what the cursor is on.
	status string
	failed bool
	width  int
	height int
}

// NewEditor builds the editor over the bindings as they are currently bound.
// The bindings are held by pointer, so rebinding one updates the list in
// place.
func NewEditor() *Editor {
	return &Editor{bindings: Bindings()}
}

func (e *Editor) SetSize(width, height int) {
	e.width = width
	e.height = height
}

// HandleKey processes one key press, reporting whether the editor should
// close. It consumes every key while open: in capture mode the next press is
// the new binding, so nothing may reach the application underneath.
func (e *Editor) HandleKey(msg tea.KeyPressMsg) (done bool) {
	if len(e.bindings) == 0 {
		return true
	}
	pressed := msg.String()
	if e.capturing {
		e.capturing = false
		// esc is how a capture is abandoned, and so is never bindable.
		if pressed == "esc" {
			e.setStatus("cancelled", false)
			return false
		}
		binding := e.bindings[e.cursor]
		if err := rebind(binding, pressed); err != nil {
			e.setStatus(err.Error(), true)
			return false
		}
		e.setStatus(fmt.Sprintf("%s is now %s", binding.ID, pressed), false)
		return false
	}

	switch pressed {
	case "esc", "ctrl+c":
		return true
	case "up", "ctrl+p":
		e.move(-1)
	case "down", "ctrl+n":
		e.move(1)
	case "enter":
		e.capturing = true
		e.status = ""
	}
	return false
}

func (e *Editor) move(delta int) {
	e.cursor = min(max(e.cursor+delta, 0), len(e.bindings)-1)
	e.status = ""
}

func (e *Editor) setStatus(status string, failed bool) {
	e.status = status
	e.failed = failed
}

func (e *Editor) View() string {
	if len(e.bindings) == 0 {
		// Unreachable with the binary's bindings registered, but a modal that
		// panics would take the whole TUI with it.
		return lipgloss.Place(e.width, e.height, lipgloss.Center, lipgloss.Center,
			styles.ConfirmBoxStyle.Render(styles.DimTextStyle.Render("no key bindings declared")))
	}
	visibleRows := max(editorMinRows, e.height-editorChrome)
	// Scroll so the cursor stays in the window.
	top := 0
	if e.cursor >= visibleRows {
		top = e.cursor - visibleRows + 1
	}

	var b strings.Builder
	b.WriteString(styles.ConfirmTitleStyle.Render("Edit Key Bindings"))
	b.WriteString("\n\n")
	for index := top; index < len(e.bindings) && index < top+visibleRows; index++ {
		b.WriteString(e.renderBinding(e.bindings[index]))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(e.renderFooter())
	return lipgloss.Place(e.width, e.height, lipgloss.Center, lipgloss.Center, styles.ConfirmBoxStyle.Render(b.String()))
}

func (e *Editor) renderBinding(binding *Binding) string {
	selected := binding == e.bindings[e.cursor]

	keys := binding.KeysString()
	switch {
	case selected && e.capturing:
		keys = "press a key..."
	case keys == "":
		// Unbound in the keymap file: no way to reach it until it is rebound.
		keys = "unbound"
	}

	// The help text takes whatever the terminal has left over.
	helpWidth := max(20, e.width-editorIDWidth-editorKeysWidth-editorChrome)
	line := fmt.Sprintf(
		"%-*s %-*s %s",
		editorIDWidth, styles.Truncate(binding.ID, editorIDWidth),
		editorKeysWidth, styles.Truncate(keys, editorKeysWidth),
		styles.Truncate(binding.Help, helpWidth),
	)
	if selected {
		return styles.MenuSelectedStyle.Render(line)
	}
	return styles.MenuItemStyle.Render(line)
}

func (e *Editor) renderFooter() string {
	var b strings.Builder
	if e.status != "" {
		style := styles.SuccessStyle
		if e.failed {
			style = styles.ErrorStyle
		}
		b.WriteString(style.Render(e.status))
		b.WriteString("\n")
	}
	if e.capturing {
		b.WriteString(styles.DimTextStyle.Render("press the key to bind │ esc: cancel"))
		return b.String()
	}
	b.WriteString(styles.DimTextStyle.Render("↑/↓: move │ enter: rebind │ esc: close"))
	if configPath != "" {
		b.WriteString("\n")
		b.WriteString(styles.DimTextStyle.Render("saved to " + configPath))
	}
	return b.String()
}
