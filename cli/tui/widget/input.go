package widget

import (
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"

	"github.com/malonaz/sgpt/cli/tui/editor"
	"github.com/malonaz/sgpt/cli/tui/keymap"
	"github.com/malonaz/sgpt/cli/tui/styles"
	"github.com/malonaz/sgpt/internal/history"
)

var (
	keyInputPrevHistory = keymap.New("alt+p", "Previous history entry")
	keyInputNextHistory = keymap.New("alt+n", "Next history entry")
	keyInputOpenEditor  = keymap.New("alt+o", "Compose in $EDITOR")
)

func InputKeymap() keymap.Map {
	return keymap.Map{
		Name:     "Input",
		Bindings: []keymap.Binding{keyInputPrevHistory, keyInputNextHistory, keyInputOpenEditor},
	}
}

type Input struct {
	Textarea textarea.Model
	history  *history.History
	width    int
}

func NewInput() *Input {
	ta := textarea.New()
	ta.Placeholder = "Type your message... (ctrl+j: send, tab: navigate, alt+h: help)"
	ta.CharLimit = 0
	ta.SetWidth(styles.DefaultTextareaWidth)
	ta.SetHeight(styles.MinTextareaHeight)
	ta.ShowLineNumbers = false
	ta.KeyMap.InsertNewline.SetEnabled(true)
	ta.Prompt = ""

	return &Input{
		Textarea: ta,
		history:  history.NewHistory(),
	}
}

func (i *Input) SetWidth(width int) {
	i.width = width
	i.Textarea.SetWidth(width - styles.TextAreaStyle.GetHorizontalPadding() - styles.TextAreaStyle.GetHorizontalBorderSize())
}

func (i *Input) Focus() tea.Cmd {
	i.Textarea.Focus()
	return textarea.Blink
}

func (i *Input) Blur() {
	i.Textarea.Blur()
}

func (i *Input) Value() string {
	return strings.TrimSpace(i.Textarea.Value())
}

func (i *Input) SetValue(text string) {
	i.Textarea.SetValue(text)
	i.Textarea.CursorEnd()
	i.AdjustHeight()
}

func (i *Input) Reset() {
	i.Textarea.Reset()
	i.AdjustHeight()
}

func (i *Input) Submit() string {
	value := i.Value()
	if value == "" {
		return ""
	}
	i.history.Add(value)
	i.history.Reset()
	i.Reset()
	return value
}

func (i *Input) Height() int {
	return i.Textarea.Height() + styles.TextAreaStyle.GetVerticalFrameSize()
}

func (i *Input) AdjustHeight() {
	content := i.Textarea.Value()
	lineCount := strings.Count(content, "\n") + 1
	newHeight := lineCount
	if newHeight < styles.MinTextareaHeight {
		newHeight = styles.MinTextareaHeight
	}
	if newHeight > styles.MaxTextareaHeight {
		newHeight = styles.MaxTextareaHeight
	}
	i.Textarea.SetHeight(newHeight)
}

func (i *Input) HandleKey(msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case key.Matches(msg, keyInputPrevHistory.Key):
		if entry, ok := i.history.Previous(i.Textarea.Value()); ok {
			i.Textarea.SetValue(entry)
			i.AdjustHeight()
		}
		return nil
	case key.Matches(msg, keyInputNextHistory.Key):
		if entry, ok := i.history.Next(); ok {
			i.Textarea.SetValue(entry)
			i.AdjustHeight()
		}
		return nil
	case key.Matches(msg, keyInputOpenEditor.Key):
		return editor.Open(i.Textarea.Value(), "md")
	}
	return nil
}

func (i *Input) Update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	i.Textarea, cmd = i.Textarea.Update(msg)
	i.AdjustHeight()
	return cmd
}

func (i *Input) View() string {
	return styles.TextAreaStyle.Render(i.Textarea.View())
}
