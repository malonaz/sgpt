package keymap

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/malonaz/sgpt/cli/tui/styles"
)

// Binding pairs a key binding with human-readable help text so the help
// modal and the contextual hints share one source of truth with key matching.
type Binding struct {
	Key  key.Binding
	Help string
	// Short is the one- or two-word action shown in contextual hints.
	Short string
}

func New(keys string, help string) Binding {
	return Binding{Key: key.NewBinding(key.WithKeys(keys)), Help: help}
}

// WithShort returns the binding with a short hint label.
func (b Binding) WithShort(short string) Binding {
	b.Short = short
	return b
}

// Label is the binding's keys as shown to the user.
func (b Binding) Label() string {
	return strings.Join(b.Key.Keys(), " / ")
}

// Hint renders the binding as a contextual hint ("enter open"): only the
// primary key, since hints are a reminder and the help modal lists them all.
func (b Binding) Hint() string {
	action := b.Short
	if action == "" {
		action = b.Help
	}
	keys := b.Key.Keys()
	if len(keys) == 0 {
		return ""
	}
	return styles.Hint(keys[0], action)
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

const (
	helpColumnGap   = 4
	helpKeyGap      = 2
	helpMaxKeyWidth = 14
	// helpMinActionWidth keeps wrapped actions readable; narrower columns
	// would wrap every word.
	helpMinActionWidth = 18
	helpMaxActionWidth = 40
)

var (
	helpBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(styles.ColorSubtle).
			Padding(1, 2)
	helpTitleStyle     = lipgloss.NewStyle().Foreground(styles.ColorText).Bold(true)
	helpSectionStyle   = lipgloss.NewStyle().Foreground(styles.ColorMuted).Bold(true)
	helpContinuedStyle = lipgloss.NewStyle().Foreground(styles.ColorSubtle).Bold(true)
	helpKeyStyle       = lipgloss.NewStyle().Foreground(styles.ColorText)
	helpActionStyle    = lipgloss.NewStyle().Foreground(styles.ColorMuted)
)

// helpUnit is an unbreakable run of lines: a section header or one binding
// (whose action may wrap).
type helpUnit struct {
	lines   []string
	header  bool
	section string
}

// RenderHelpPage renders a centered help modal that fits width×height:
// sections flow into side-by-side columns, and whatever still doesn't fit
// spills onto further pages. It returns the page count.
func RenderHelpPage(maps []Map, width, height, page int) (string, int) {
	box := helpBoxStyle
	if height < 30 {
		box = box.Padding(0, 2)
	}
	// Title, blank, blank, footer.
	maxHeight := max(1, height-box.GetVerticalFrameSize()-4)
	available := max(1, width-box.GetHorizontalFrameSize())

	keyWidth := 0
	for _, keymapEntry := range maps {
		for _, binding := range keymapEntry.Bindings {
			keyWidth = max(keyWidth, lipgloss.Width(binding.Label()))
		}
	}
	keyWidth = min(keyWidth, helpMaxKeyWidth)
	minColumn := keyWidth + helpKeyGap + helpMinActionWidth
	maxColumn := keyWidth + helpKeyGap + helpMaxActionWidth
	maxColumns := max(1, min(len(maps), (available+helpColumnGap)/(minColumn+helpColumnGap)))

	// The fewest columns that fit on one page: wider columns wrap less.
	var columns [][]string
	var columnCount, columnWidth int
	for columnCount = 1; columnCount <= maxColumns; columnCount++ {
		columnWidth = min(maxColumn, (available+helpColumnGap)/columnCount-helpColumnGap)
		units := helpUnits(maps, keyWidth, columnWidth-keyWidth-helpKeyGap)
		columns = packHelpColumns(units, balancedHelpHeight(units, columnCount, maxHeight))
		if len(columns) <= columnCount {
			break
		}
	}
	columnCount = min(columnCount, maxColumns)

	pages := (len(columns) + columnCount - 1) / columnCount
	page = min(max(page, 0), pages-1)
	pageColumns := columns[page*columnCount : min(len(columns), (page+1)*columnCount)]

	rendered := make([]string, 0, 2*len(pageColumns))
	for i, column := range pageColumns {
		if i > 0 {
			rendered = append(rendered, strings.Repeat(" ", helpColumnGap))
		}
		rendered = append(rendered, lipgloss.NewStyle().Width(columnWidth).Render(strings.Join(column, "\n")))
	}

	hints := []string{styles.Hint("esc", "close")}
	if pages > 1 {
		hints = append([]string{styles.MutedStyle.Render(fmt.Sprintf("page %d/%d", page+1, pages)), styles.Hint("pgdn", "more")}, hints...)
	}
	content := helpTitleStyle.Render("Keyboard shortcuts") + "\n\n" +
		lipgloss.JoinHorizontal(lipgloss.Top, rendered...) + "\n\n" +
		styles.Hints(hints...)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box.Render(content)), pages
}

func helpUnits(maps []Map, keyWidth, actionWidth int) []helpUnit {
	var units []helpUnit
	for _, keymapEntry := range maps {
		units = append(units, helpUnit{lines: []string{helpSectionStyle.Render(keymapEntry.Name)}, header: true, section: keymapEntry.Name})
		for _, binding := range keymapEntry.Bindings {
			label := ansi.Truncate(binding.Label(), keyWidth, styles.GlyphEllipsis)
			// Wrap rather than truncate: the help modal is where full
			// descriptions belong.
			actionLines := strings.Split(ansi.Wordwrap(binding.Help, actionWidth, ""), "\n")
			lines := make([]string, len(actionLines))
			for i, action := range actionLines {
				key := ""
				if i == 0 {
					key = label
				}
				lines[i] = helpKeyStyle.Render(fmt.Sprintf("%-*s", keyWidth, key)) +
					strings.Repeat(" ", helpKeyGap) + helpActionStyle.Render(ansi.Truncate(action, actionWidth, ""))
			}
			units = append(units, helpUnit{lines: lines, section: keymapEntry.Name})
		}
	}
	return units
}

// balancedHelpHeight picks the shortest column height that still fits every
// unit into columnCount columns, so a single page reads evenly instead of
// one tall column and a stub.
func balancedHelpHeight(units []helpUnit, columnCount, maxHeight int) int {
	total := 0
	for _, unit := range units {
		total += len(unit.lines)
		if unit.header {
			total++
		}
	}
	for height := max(1, total/columnCount); height < maxHeight; height++ {
		if len(packHelpColumns(units, height)) <= columnCount {
			return height
		}
	}
	return maxHeight
}

// packHelpColumns flows units into columns of at most height lines. Sections
// are separated by a blank line, a header never ends a column, and a column
// that opens mid-section repeats the section name.
func packHelpColumns(units []helpUnit, height int) [][]string {
	var columns [][]string
	var column []string
	for i, unit := range units {
		need := len(unit.lines)
		if unit.header && len(column) > 0 {
			need++
		}
		if unit.header && i+1 < len(units) {
			need += len(units[i+1].lines)
		}
		if len(column) > 0 && len(column)+need > height {
			columns = append(columns, column)
			column = nil
			if !unit.header {
				column = append(column, helpContinuedStyle.Render(unit.section+" "+styles.GlyphEllipsis))
			}
		}
		if unit.header && len(column) > 0 {
			column = append(column, "")
		}
		column = append(column, unit.lines...)
	}
	if len(column) > 0 {
		columns = append(columns, column)
	}
	if len(columns) == 0 {
		columns = [][]string{nil}
	}
	return columns
}
