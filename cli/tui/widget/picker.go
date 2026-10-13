package widget

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/malonaz/sgpt/cli/tui/keymap"
	"github.com/malonaz/sgpt/cli/tui/styles"
)

const (
	pickerMaxWidth = 72
	pickerMaxRows  = 14
)

var (
	pickerKeyUp     = keymap.Binding{Key: key.NewBinding(key.WithKeys("ctrl+p", "up")), Help: "Move up", Short: "up"}
	pickerKeyDown   = keymap.Binding{Key: key.NewBinding(key.WithKeys("ctrl+n", "down")), Help: "Move down", Short: "down"}
	pickerKeyToggle = keymap.Binding{Key: key.NewBinding(key.WithKeys("space")), Help: "Toggle", Short: "toggle"}
	pickerKeyAll    = keymap.New("ctrl+a", "Select all visible").WithShort("all")
	// ctrl+x: free in the filter field, and "cut" reads as clear.
	pickerKeyClear  = keymap.New("ctrl+x", "Clear selection").WithShort("none")
	pickerKeyApply  = keymap.Binding{Key: key.NewBinding(key.WithKeys("enter", "ctrl+j")), Help: "Apply", Short: "apply"}
	pickerKeyCancel = keymap.Binding{Key: key.NewBinding(key.WithKeys("esc", "ctrl+c")), Help: "Cancel", Short: "cancel"}

	pickerBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(styles.ColorBrand).
			Padding(1, 2)
	pickerTitleStyle = lipgloss.NewStyle().Foreground(styles.ColorText).Bold(true)
)

// PickerItem is one selectable entry in a Picker.
type PickerItem struct {
	Label    string
	Selected bool
}

// pickerMatch is an item that passes the filter, with the byte offsets of
// the matched characters for highlighting.
type pickerMatch struct {
	index     int
	score     int
	positions []int
}

// Picker is a modal fuzzy-search multi-select (fzf-style): type to filter,
// move, toggle, apply or cancel. The owning screen routes keys to it and
// applies the result on close.
type Picker struct {
	title   string
	input   textarea.Model
	items   []PickerItem
	matches []pickerMatch
	cursor  int
	top     int
	width   int
	height  int
}

func NewPicker(title string, items []PickerItem) *Picker {
	input := NewFilterInput("Filter" + styles.GlyphEllipsis)
	input.Focus()
	picker := &Picker{title: title, items: items, input: input}
	picker.filter()
	return picker
}

func (p *Picker) SetSize(width, height int) {
	p.width = width
	p.height = height
	p.input.SetWidth(p.innerWidth())
}

func (p *Picker) innerWidth() int {
	return max(10, min(pickerMaxWidth, p.width-4)-pickerBoxStyle.GetHorizontalFrameSize())
}

// visibleRows is how many items fit: the modal never outgrows the screen.
func (p *Picker) visibleRows() int {
	// Box frame, title, input, rule, footer and their spacing.
	return max(1, min(pickerMaxRows, len(p.items), p.height-2-pickerBoxStyle.GetVerticalFrameSize()-6))
}

// Selected returns the labels currently toggled on.
func (p *Picker) Selected() []string {
	var selected []string
	for _, item := range p.items {
		if item.Selected {
			selected = append(selected, item.Label)
		}
	}
	return selected
}

// HandleKey processes one key press; done is true when the picker should
// close, canceled when the pending selection must be discarded.
func (p *Picker) HandleKey(msg tea.KeyPressMsg) (done, canceled bool) {
	switch {
	case key.Matches(msg, pickerKeyCancel.Key):
		return true, true
	case key.Matches(msg, pickerKeyApply.Key):
		return true, false
	case key.Matches(msg, pickerKeyUp.Key):
		p.cursor = max(0, p.cursor-1)
	case key.Matches(msg, pickerKeyDown.Key):
		p.cursor = max(0, min(len(p.matches)-1, p.cursor+1))
	case key.Matches(msg, pickerKeyToggle.Key):
		if p.cursor < len(p.matches) {
			item := &p.items[p.matches[p.cursor].index]
			item.Selected = !item.Selected
		}
	case key.Matches(msg, pickerKeyAll.Key):
		for _, match := range p.matches {
			p.items[match.index].Selected = true
		}
	case key.Matches(msg, pickerKeyClear.Key):
		for i := range p.items {
			p.items[i].Selected = false
		}
	default:
		p.input, _ = p.input.Update(msg)
		p.filter()
	}
	p.scrollToCursor()
	return false, false
}

func (p *Picker) scrollToCursor() {
	rows := p.visibleRows()
	p.top = min(p.top, p.cursor)
	if p.cursor >= p.top+rows {
		p.top = p.cursor - rows + 1
	}
	p.top = max(0, min(p.top, len(p.matches)-rows))
}

func (p *Picker) filter() {
	query := strings.TrimSpace(p.input.Value())
	p.matches = p.matches[:0]
	for i, item := range p.items {
		if score, positions, ok := fuzzyMatch(item.Label, query); ok {
			p.matches = append(p.matches, pickerMatch{index: i, score: score, positions: positions})
		}
	}
	// Stable: equal scores keep the caller's ordering (e.g. injected first).
	sort.SliceStable(p.matches, func(a, b int) bool { return p.matches[a].score < p.matches[b].score })
	p.cursor = min(p.cursor, max(0, len(p.matches)-1))
	p.top = 0
}

// fuzzyMatch reports whether query is a case-insensitive subsequence of
// candidate, with the byte offsets of the matched runes. Lower scores rank
// higher: tighter and earlier matches win.
func fuzzyMatch(candidate, query string) (int, []int, bool) {
	if query == "" {
		return 0, nil, true
	}
	var positions []int
	queryRunes := []rune(strings.ToLower(query))
	next := 0
	for offset, r := range candidate {
		if next < len(queryRunes) && unicode.ToLower(r) == queryRunes[next] {
			positions = append(positions, offset)
			next++
		}
	}
	if next < len(queryRunes) {
		return 0, nil, false
	}
	start, end := positions[0], positions[len(positions)-1]
	// Scattered matches (large span) are penalized harder than late ones.
	return (end-start+1-len(positions))*4 + start, positions, true
}

func (p *Picker) View() string {
	width := p.innerWidth()
	rows := p.visibleRows()

	counts := styles.MutedStyle.Render(fmt.Sprintf("%d selected %s %d/%d", len(p.Selected()), styles.GlyphSeparator, len(p.matches), len(p.items)))
	title := pickerTitleStyle.Render(p.title)
	header := title + strings.Repeat(" ", max(1, width-lipgloss.Width(title)-lipgloss.Width(counts))) + counts

	lines := []string{header, "", p.input.View(), styles.SubtleStyle.Render(strings.Repeat("─", width))}
	for row := p.top; row < p.top+rows; row++ {
		switch {
		case row < len(p.matches):
			lines = append(lines, p.renderRow(p.matches[row], row == p.cursor, width))
		case row == 0:
			lines = append(lines, styles.MutedStyle.Render("  No matches"))
		default:
			lines = append(lines, "")
		}
	}
	more := ""
	if hidden := len(p.matches) - p.top - rows; hidden > 0 {
		more = styles.SubtleStyle.Render(fmt.Sprintf("  %s %d more", styles.GlyphEllipsis, hidden))
	}
	lines = append(lines, more, styles.Hints(
		pickerKeyToggle.Hint(), pickerKeyAll.Hint(), pickerKeyClear.Hint(), pickerKeyApply.Hint(), pickerKeyCancel.Hint(),
	))
	return lipgloss.Place(p.width, p.height, lipgloss.Center, lipgloss.Center, pickerBoxStyle.Render(strings.Join(lines, "\n")))
}

// renderRow draws one item: the cursor row on Surface behind a brand bar,
// matched characters in brand.
func (p *Picker) renderRow(match pickerMatch, current bool, width int) string {
	base := lipgloss.NewStyle()
	bar := " "
	if current {
		base = base.Background(styles.ColorSurface)
		bar = lipgloss.NewStyle().Foreground(styles.ColorBrand).Background(styles.ColorSurface).Render(styles.GlyphSelected)
	}
	item := p.items[match.index]
	check := base.Foreground(styles.ColorSubtle).Render("○")
	if item.Selected {
		check = base.Foreground(styles.ColorBrand).Render(styles.GlyphSuccess)
	}

	label := ansi.Truncate(item.Label, width-4, styles.GlyphEllipsis)
	textStyle := base.Foreground(styles.ColorText)
	matchStyle := base.Foreground(styles.ColorBrand).Bold(true)
	// Render runs of matched/unmatched runes, not each rune, to keep the
	// escape sequences down.
	var b, run strings.Builder
	runMatched := false
	positions := match.positions
	for offset, r := range label {
		matched := len(positions) > 0 && positions[0] == offset
		if matched {
			positions = positions[1:]
		}
		if matched != runMatched && run.Len() > 0 {
			b.WriteString(pickerRunStyle(textStyle, matchStyle, runMatched).Render(run.String()))
			run.Reset()
		}
		runMatched = matched
		run.WriteRune(r)
	}
	b.WriteString(pickerRunStyle(textStyle, matchStyle, runMatched).Render(run.String()))
	row := bar + base.Render(" ") + check + base.Render(" ") + b.String()
	return row + base.Render(strings.Repeat(" ", max(0, width-lipgloss.Width(row))))
}

func pickerRunStyle(text, matched lipgloss.Style, isMatch bool) lipgloss.Style {
	if isMatch {
		return matched
	}
	return text
}
