package widget

import (
	"os"
	"os/exec"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"

	"github.com/malonaz/sgpt/cli/tui/styles"
	"github.com/malonaz/sgpt/internal/history"
)

var (
	keyInputPrevHistory = key.NewBinding(key.WithKeys("alt+p"))
	keyInputNextHistory = key.NewBinding(key.WithKeys("alt+n"))
	keyInputOpenEditor  = key.NewBinding(key.WithKeys("ctrl+o"))
)

// EditorClosedMsg is sent when the external editor process exits.
type EditorClosedMsg struct {
	Modified bool
	Content  string
}

// Input wraps a textarea with history and editor support.
type Input struct {
	Textarea textarea.Model
	history  *history.History
	width    int
}

func NewInput() *Input {
	ta := textarea.New()
	ta.Placeholder = "Type your message... (Ctrl+J to send, Tab to navigate)"
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

func (i *Input) Reset() {
	i.Textarea.Reset()
	i.AdjustHeight()
}

// Submit returns the trimmed value and records it in history.
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

// HandleKey processes input-specific keys. Returns a cmd if handled, nil otherwise.
func (i *Input) HandleKey(msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case key.Matches(msg, keyInputPrevHistory):
		if entry, ok := i.history.Previous(i.Textarea.Value()); ok {
			i.Textarea.SetValue(entry)
			i.AdjustHeight()
		}
		return nil
	case key.Matches(msg, keyInputNextHistory):
		if entry, ok := i.history.Next(); ok {
			i.Textarea.SetValue(entry)
			i.AdjustHeight()
		}
		return nil
	case key.Matches(msg, keyInputOpenEditor):
		return i.openEditor(i.Textarea.Value(), "md")
	}
	return nil
}

// Update delegates to the underlying textarea.
func (i *Input) Update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	i.Textarea, cmd = i.Textarea.Update(msg)
	i.AdjustHeight()
	return cmd
}

func (i *Input) View() string {
	return styles.TextAreaStyle.Render(i.Textarea.View())
}

func (i *Input) openEditor(content, ext string) tea.Cmd {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		editor = "vim"
	}
	editorArgs := strings.Fields(editor)
	if len(editorArgs) == 0 {
		return nil
	}

	tmpFile, err := os.CreateTemp("", "sgpt-*."+ext)
	if err != nil {
		return nil
	}
	tmpPath := tmpFile.Name()

	if content != "" {
		tmpFile.WriteString(content)
	}
	tmpFile.Close()

	info, _ := os.Stat(tmpPath)
	modTimeBefore := info.ModTime()

	args := append(editorArgs[1:], tmpPath)
	cmd := exec.Command(editorArgs[0], args...)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		info, statErr := os.Stat(tmpPath)
		if statErr != nil {
			return EditorClosedMsg{}
		}
		bytes, readErr := os.ReadFile(tmpPath)
		os.Remove(tmpPath)
		if readErr != nil {
			return EditorClosedMsg{}
		}
		return EditorClosedMsg{
			Modified: info.ModTime().After(modTimeBefore),
			Content:  string(bytes),
		}
	})
}
