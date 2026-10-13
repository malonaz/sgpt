package widget

import (
	"strings"
	"unicode"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/malonaz/sgpt/cli/tui/editor"
	"github.com/malonaz/sgpt/cli/tui/keymap"
	"github.com/malonaz/sgpt/cli/tui/styles"
	"github.com/malonaz/sgpt/internal/history"
)

const (
	minInputLines = 1
	maxInputLines = 20
	promptWidth   = 2
)

var (
	keyInputPrevHistory = keymap.New("alt+p", "Previous history entry")
	keyInputNextHistory = keymap.New("alt+n", "Next history entry")
	keyInputOpenEditor  = keymap.New("alt+o", "Compose in $EDITOR")
)

var (
	// The border is the focus cue: brand while typing, subtle otherwise.
	inputFocusedStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(styles.ColorBrand).
				Padding(0, 1)
	inputBlurredStyle = inputFocusedStyle.BorderForeground(styles.ColorSubtle)

	inputPromptFocused = lipgloss.NewStyle().Foreground(styles.ColorBrand).Bold(true).Render(styles.GlyphPrompt)
	inputPromptBlurred = styles.SubtleStyle.Render(styles.GlyphPrompt)
)

func InputKeymap() keymap.Map {
	return keymap.Map{
		Name:     "Input",
		Bindings: []keymap.Binding{keyInputPrevHistory, keyInputNextHistory, keyInputOpenEditor},
	}
}

type Input struct {
	Textarea textarea.Model
	history  *history.Cursor
	// maxLines caps the text rows so the input never crowds out the timeline.
	maxLines int
}

func NewInput(store *history.Store) *Input {
	ta := textarea.New()
	ta.CharLimit = 0
	ta.ShowLineNumbers = false
	ta.KeyMap.InsertNewline.SetEnabled(true)
	ta.SetPromptFunc(promptWidth, func(info textarea.PromptInfo) string {
		switch {
		case info.LineNumber > 0:
			return "  "
		case info.Focused:
			return inputPromptFocused + " "
		}
		return inputPromptBlurred + " "
	})
	textStyles := ta.Styles()
	for _, state := range []*textarea.StyleState{&textStyles.Focused, &textStyles.Blurred} {
		state.Base = lipgloss.NewStyle()
		state.CursorLine = lipgloss.NewStyle()
		state.Text = styles.TextStyle
		state.Placeholder = styles.MutedStyle
		state.EndOfBuffer = lipgloss.NewStyle()
	}
	textStyles.Cursor.Color = styles.ColorBrand
	ta.SetStyles(textStyles)
	ta.SetHeight(minInputLines)

	return &Input{Textarea: ta, history: store.NewCursor(), maxLines: maxInputLines}
}

// SetWidth sizes the input to the full width, frame included.
func (i *Input) SetWidth(width int) {
	i.Textarea.SetWidth(width - inputFocusedStyle.GetHorizontalFrameSize())
	i.AdjustHeight()
}

// SetMaxHeight caps the input's total height, frame included.
func (i *Input) SetMaxHeight(height int) {
	i.maxLines = max(minInputLines, min(maxInputLines, height-inputFocusedStyle.GetVerticalFrameSize()))
	i.AdjustHeight()
}

func (i *Input) SetPlaceholder(placeholder string) {
	i.Textarea.Placeholder = placeholder
}

func (i *Input) Focus() tea.Cmd {
	return i.Textarea.Focus()
}

func (i *Input) Blur() {
	i.Textarea.Blur()
}

func (i *Input) Value() string {
	return strings.TrimSpace(i.Textarea.Value())
}

func (i *Input) SetValue(text string) {
	i.edit(func() {
		i.Textarea.SetValue(text)
		i.Textarea.CursorEnd()
	})
}

func (i *Input) Reset() {
	i.edit(i.Textarea.Reset)
}

// edit applies a change at full height, then fits the height to the result:
// a change made at the old height would scroll the textarea, and shrinking
// never scrolls it back.
func (i *Input) edit(change func()) {
	i.Textarea.SetHeight(i.maxLines)
	change()
	i.AdjustHeight()
}

func (i *Input) Submit() string {
	value := i.Value()
	if value == "" {
		return ""
	}
	i.history.Add(value)
	i.Reset()
	return value
}

// Height is the input's total height, frame included.
func (i *Input) Height() int {
	return i.Textarea.Height() + inputFocusedStyle.GetVerticalFrameSize()
}

// AdjustHeight fits the textarea to its content, soft-wrapped rows included.
func (i *Input) AdjustHeight() {
	width := i.Textarea.Width()
	rows := 0
	for _, line := range strings.Split(i.Textarea.Value(), "\n") {
		rows += wrappedRows(line, width)
	}
	i.Textarea.SetHeight(max(minInputLines, min(i.maxLines, rows)))
}

// wrappedRows counts the rows the textarea soft-wraps line into. It mirrors
// the textarea's word wrap, which keeps a row free for the cursor once a line
// fills the width.
func wrappedRows(line string, width int) int {
	if width <= 0 {
		return 1
	}
	rows, rowWidth, wordWidth, spaces := 1, 0, 0, 0
	for _, r := range line {
		if unicode.IsSpace(r) {
			spaces++
		} else {
			runeWidth := ansi.StringWidth(string(r))
			wordWidth += runeWidth
			if spaces == 0 && wordWidth+runeWidth > width {
				// A word wider than the row is broken onto its own rows.
				if rowWidth > 0 {
					rows++
					rowWidth = 0
				}
				rowWidth += wordWidth
				wordWidth = 0
				continue
			}
		}
		if spaces > 0 {
			if rowWidth+wordWidth+spaces > width {
				rows++
				rowWidth = 0
			}
			rowWidth += wordWidth + spaces
			wordWidth, spaces = 0, 0
		}
	}
	if rowWidth+wordWidth+spaces >= width {
		rows++
	}
	return rows
}

func (i *Input) HandleKey(msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case key.Matches(msg, keyInputPrevHistory.Key):
		if entry, ok := i.history.Previous(i.Textarea.Value()); ok {
			i.SetValue(entry)
		}
		return nil
	case key.Matches(msg, keyInputNextHistory.Key):
		if entry, ok := i.history.Next(); ok {
			i.SetValue(entry)
		}
		return nil
	case key.Matches(msg, keyInputOpenEditor.Key):
		return editor.Open(i.Textarea.Value(), "md")
	}
	return nil
}

func (i *Input) Update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	i.edit(func() { i.Textarea, cmd = i.Textarea.Update(msg) })
	return cmd
}

func (i *Input) View() string {
	style := inputBlurredStyle
	if i.Textarea.Focused() {
		style = inputFocusedStyle
	}
	return style.Render(i.Textarea.View())
}
