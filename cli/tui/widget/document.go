package widget

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/malonaz/sgpt/cli/tui/styles"
	"github.com/malonaz/sgpt/internal/markdown"
)

// documentMaxWidth keeps prose a readable measure on wide terminals.
const documentMaxWidth = 100

var (
	documentKeyClose = key.NewBinding(key.WithKeys("esc"))
	// The body scrolls, so vertical padding would only cost reading lines.
	documentBoxStyle = infoBoxStyle.Padding(0, 2)
)

// Document is a modal reading pane over rendered markdown: what is too long
// to sit in the timeline, opened on demand.
type Document struct {
	title    string
	markdown string
	width    int
	height   int
	viewport viewport.Model
}

// NewDocument renders markdown into a modal sized for a width×height screen.
func NewDocument(title, markdown string, width, height int) *Document {
	d := &Document{title: title, markdown: markdown, viewport: viewport.New()}
	// Match the timeline's scroll keys on top of the pager ones.
	d.viewport.KeyMap.Up.SetKeys(append(d.viewport.KeyMap.Up.Keys(), "ctrl+p")...)
	d.viewport.KeyMap.Down.SetKeys(append(d.viewport.KeyMap.Down.Keys(), "ctrl+n")...)
	d.SetSize(width, height)
	return d
}

// SetSize re-renders at the new width: wrapping depends on it.
func (d *Document) SetSize(width, height int) {
	d.width, d.height = width, height
	innerWidth := max(20, min(documentMaxWidth, width-2)-documentBoxStyle.GetHorizontalFrameSize())
	content := d.markdown
	if renderer, err := markdown.NewRenderer(innerWidth); err == nil {
		content = renderer.ToMarkdown(-1, false, markdown.ParseBlocks(d.markdown)...)
	}
	content = strings.Trim(content, "\n")
	// Title, footer and the blank lines between them and the body.
	chrome := 4 + documentBoxStyle.GetVerticalFrameSize()
	d.viewport.SetWidth(innerWidth)
	d.viewport.SetHeight(max(1, min(lipgloss.Height(content), height-2-chrome)))
	d.viewport.SetContent(content)
}

// HandleKey scrolls the document; it reports false once the key closes it.
func (d *Document) HandleKey(msg tea.KeyPressMsg) (open bool) {
	if key.Matches(msg, documentKeyClose) {
		return false
	}
	d.viewport, _ = d.viewport.Update(msg)
	return true
}

func (d *Document) View() string {
	innerWidth := d.viewport.Width()
	title := infoTitleStyle.Render(ansi.Truncate(d.title, innerWidth, styles.GlyphEllipsis))
	hints := []string{styles.Hint("esc", "close")}
	if d.viewport.TotalLineCount() > d.viewport.Height() {
		hints = append(hints, styles.Hint("↑↓ pgup pgdn", "scroll"),
			styles.MutedStyle.Render(fmt.Sprintf("%.0f%%", d.viewport.ScrollPercent()*100)))
	}
	modal := documentBoxStyle.Render(title + "\n\n" + d.viewport.View() + "\n\n" + styles.Hints(hints...))
	return lipgloss.Place(d.width, d.height, lipgloss.Center, lipgloss.Center, modal)
}
