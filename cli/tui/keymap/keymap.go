package keymap

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"

	"github.com/malonaz/sgpt/cli/tui/styles"
)

// Binding pairs a key binding with human-readable help text so the help
// modal is generated from the same source of truth as key matching.
//
// Bindings are declared once at package initialization and registered by ID.
// Load rebinds them in place, so every holder of the pointer picks up the
// user's mapping without further wiring.
//
// Bindings are global: one key means one thing everywhere. An action several
// screens share is therefore one binding they all hold, not one per screen —
// see the shared bindings in this package.
type Binding struct {
	// ID addresses this binding in the user's keymap file. Stable across
	// releases: renaming one silently drops everyone's override of it.
	ID   string
	Help string
	Key  key.Binding
}

// KeysString renders the bound keys for display, e.g. "alt+p / ctrl+p".
// Empty when the user unbound the binding.
func (b *Binding) KeysString() string {
	return strings.Join(b.Key.Keys(), " / ")
}

// registry holds every declared binding, keyed by ID. Populated by New at
// package initialization, so it is complete by the time main runs.
var registry = map[string]*Binding{}

// New declares a user-configurable binding. Panics on a duplicate ID or on
// missing default keys: every argument is a compile-time constant, so a
// panic here is a programming error.
func New(id, help string, keys ...string) *Binding {
	if _, ok := registry[id]; ok {
		panic(fmt.Sprintf("keymap: duplicate binding id %q", id))
	}
	if len(keys) == 0 {
		panic(fmt.Sprintf("keymap: binding %q declares no default keys", id))
	}
	binding := &Binding{ID: id, Help: help, Key: key.NewBinding(key.WithKeys(keys...))}
	registry[id] = binding
	return binding
}

// Lookup returns the binding declared under id, or nil. Lets a package
// display a key it does not own (e.g. the input placeholder naming the chat
// screen's send key) without importing it.
func Lookup(id string) *Binding {
	return registry[id]
}

// KeysOf renders the keys bound to id, for naming a key a package does not
// own (the input placeholder points at the chat screen's send key, and that
// package imports this one, not the other way round). Panics on an unknown
// id: the ids are compile-time constants, and a silent empty hint is exactly
// what this feature must not produce.
func KeysOf(id string) string {
	binding, ok := registry[id]
	if !ok {
		panic(fmt.Sprintf("keymap: unknown binding id %q", id))
	}
	return binding.KeysString()
}

// Bindings returns every declared binding, ordered by ID so the generated
// keymap file and the editor list them in the same order every run.
func Bindings() []*Binding {
	bindings := make([]*Binding, 0, len(registry))
	for _, binding := range registry {
		bindings = append(bindings, binding)
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].ID < bindings[j].ID })
	return bindings
}

// Map is a named group of bindings, rendered as a section in the help modal.
type Map struct {
	Name     string
	Bindings []*Binding
}

// Keymapper is implemented by screens that expose their bindings for help.
type Keymapper interface {
	Keymaps() []Map
}

var (
	helpBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(styles.PrimaryColor).
			Padding(1, 3)

	helpSectionStyle = lipgloss.NewStyle().
				Foreground(styles.SecondaryColor).
				Bold(true)

	helpKeyStyle = lipgloss.NewStyle().
			Foreground(styles.AccentColor)

	helpTextStyle = lipgloss.NewStyle().
			Foreground(styles.TextColor)
)

// RenderHelp renders a centered modal listing all maps.
func RenderHelp(maps []Map, width, height int) string {
	var b strings.Builder
	b.WriteString(helpSectionStyle.Render("Keyboard Shortcuts"))
	b.WriteString("\n")
	for _, keymapEntry := range maps {
		b.WriteString("\n")
		b.WriteString(helpSectionStyle.Render(keymapEntry.Name))
		b.WriteString("\n")
		for _, binding := range keymapEntry.Bindings {
			keys := binding.KeysString()
			if keys == "" {
				// Unbound in the user's keymap file: no way to reach it.
				continue
			}
			b.WriteString(fmt.Sprintf(
				"  %s  %s\n",
				helpKeyStyle.Render(fmt.Sprintf("%-16s", keys)),
				helpTextStyle.Render(binding.Help),
			))
		}
	}
	b.WriteString("\n")
	b.WriteString(styles.DimTextStyle.Render("press any key to close"))
	box := helpBoxStyle.Render(b.String())
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}
