package keymap

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"

	"github.com/malonaz/sgpt/cli/tui/styles"
)

// Binding pairs a key binding with human-readable help text so the help
// modal is generated from the same source of truth as key matching.
type Binding struct {
	Key  key.Binding
	Help string
}

func New(keys string, help string) Binding {
	return Binding{Key: key.NewBinding(key.WithKeys(keys)), Help: help}
}

// Map is a named group of bindings, rendered as a section in the help modal.
type Map struct {
	Name     string
	Bindings []Binding
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
			keys := strings.Join(binding.Key.Keys(), " / ")
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
