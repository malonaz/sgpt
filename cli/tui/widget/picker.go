package widget

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/malonaz/sgpt/cli/tui/styles"
)

// PickerItem is one selectable entry in a Picker.
type PickerItem struct {
	Label    string
	Selected bool
}

// Picker is a modal fuzzy-search multi-select (fzf-style): type to filter,
// ctrl+p/ctrl+n to move, tab to toggle, enter to apply, esc to cancel.
// The owning screen routes keys to it and applies the result on close.
type Picker struct {
	title string
	input textarea.Model
	items []PickerItem
	// matches are indices into items, filtered and ranked by fuzzyScore.
	matches []int
	cursor  int
	width   int
	height  int
}

func NewPicker(title string, items []PickerItem) *Picker {
	input := textarea.New()
	input.Placeholder = "Fuzzy filter..."
	input.SetHeight(1)
	input.ShowLineNumbers = false
	input.Prompt = ""
	input.Focus()
	picker := &Picker{title: title, items: items, input: input}
	picker.filter()
	return picker
}

func (p *Picker) SetSize(width, height int) {
	p.width = width
	p.height = height
	p.input.SetWidth(min(60, width-8))
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
	switch msg.String() {
	case "esc", "ctrl+c":
		return true, true
	case "enter", "ctrl+j":
		return true, false
	case "ctrl+p", "up":
		if p.cursor > 0 {
			p.cursor--
		}
	case "ctrl+n", "down":
		if p.cursor < len(p.matches)-1 {
			p.cursor++
		}
	// bubbletea v2 reports space as the keystroke "space" (Key.String falls
	// back to Keystroke for it), never as a literal " ".
	case " ", "space":
		if p.cursor < len(p.matches) {
			item := &p.items[p.matches[p.cursor]]
			item.Selected = !item.Selected
		}
	default:
		p.input, _ = p.input.Update(msg)
		p.filter()
	}
	return false, false
}

func (p *Picker) filter() {
	query := strings.TrimSpace(p.input.Value())
	type scoredMatch struct{ index, score int }
	var results []scoredMatch
	for i, item := range p.items {
		score, ok := fuzzyScore(item.Label, query)
		if !ok {
			continue
		}
		results = append(results, scoredMatch{index: i, score: score})
	}
	// Stable: equal scores keep the caller's ordering (e.g. injected first).
	sort.SliceStable(results, func(a, b int) bool { return results[a].score < results[b].score })
	p.matches = p.matches[:0]
	for _, result := range results {
		p.matches = append(p.matches, result.index)
	}
	if p.cursor >= len(p.matches) {
		p.cursor = max(0, len(p.matches)-1)
	}
}

// fuzzyScore reports whether query is a case-insensitive subsequence of
// candidate; lower scores rank higher (tighter and earlier matches win).
func fuzzyScore(candidate, query string) (int, bool) {
	if query == "" {
		return 0, true
	}
	candidate = strings.ToLower(candidate)
	query = strings.ToLower(query)
	start, position := -1, 0
	for _, r := range query {
		index := strings.IndexRune(candidate[position:], r)
		if index == -1 {
			return 0, false
		}
		position += index
		if start == -1 {
			start = position
		}
		position++
	}
	// Scattered matches (large span) are penalized harder than late ones.
	return (position-start-len(query))*4 + start, true
}

func (p *Picker) View() string {
	maxRows := max(5, p.height-10)
	var b strings.Builder
	b.WriteString(styles.ConfirmTitleStyle.Render(fmt.Sprintf("%s (%d selected)", p.title, len(p.Selected()))))
	b.WriteString("\n")
	b.WriteString(styles.SearchInputStyle.Render(p.input.View()))
	b.WriteString("\n")
	// Scroll the window so the cursor row stays visible.
	top := 0
	if p.cursor >= maxRows {
		top = p.cursor - maxRows + 1
	}
	for row := top; row < len(p.matches) && row < top+maxRows; row++ {
		item := p.items[p.matches[row]]
		check := "[ ]"
		if item.Selected {
			check = "[x]"
		}
		line := fmt.Sprintf("%s %s", check, item.Label)
		if row == p.cursor {
			line = styles.MenuSelectedStyle.Render(line)
		} else {
			line = styles.MenuItemStyle.Render(line)
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	if len(p.matches) == 0 {
		b.WriteString(styles.DimTextStyle.Render("no matches"))
		b.WriteString("\n")
	}
	b.WriteString(styles.DimTextStyle.Render("space: toggle │ enter: apply │ esc: cancel"))
	box := styles.ConfirmBoxStyle.Render(b.String())
	return lipgloss.Place(p.width, p.height, lipgloss.Center, lipgloss.Center, box)
}
