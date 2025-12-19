package session

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"

	"github.com/malonaz/sgpt/cli/chat/styles"
)

// adjustTextareaHeight resizes the textarea based on content line count.
func (m *Model) adjustTextareaHeight() {
	content := m.textarea.Value()
	lineCount := strings.Count(content, "\n") + 1

	newHeight := lineCount
	if newHeight < styles.MinTextareaHeight {
		newHeight = styles.MinTextareaHeight
	}
	if newHeight > styles.MaxTextareaHeight {
		newHeight = styles.MaxTextareaHeight
	}

	oldHeight := m.textarea.Height()
	if oldHeight != newHeight {
		m.textarea.SetHeight(newHeight)

		heightDiff := newHeight - oldHeight

		m.recalculateLayout()

		if heightDiff != 0 && m.ready {
			m.viewport.LineDown(heightDiff)
		}
	}
}

// scrollToNavigatedMessage scrolls the viewport to show the currently navigated message.
func (m *Model) scrollToNavigatedMessage() {
	if m.navigationMessageIndex < 0 || m.navigationMessageIndex >= len(m.messageViewportOffsets) {
		return
	}
	targetLine := m.messageViewportOffsets[m.navigationMessageIndex]
	m.viewport.SetYOffset(targetLine)
}

// recalculateLayout adjusts viewport and textarea dimensions based on current state.
func (m *Model) recalculateLayout() {
	if m.width == 0 || m.height == 0 {
		return
	}

	viewportHeight := m.height - styles.HeaderHeight
	viewportWidth := m.width

	if m.streaming {
		//viewportHeight -= styles.TextAreaStyle.GetVerticalFrameSize()
	} else {
		viewportHeight -= m.textarea.Height() + styles.InputBorderHeight
	}

	if m.err != nil {
		viewportHeight -= 1
	}

	if viewportHeight < styles.MinViewportHeight {
		viewportHeight = styles.MinViewportHeight
	}
	contentWidth := viewportWidth
	m.renderer.SetWidth(contentWidth - styles.MessageHorizontalFrameSize())

	if !m.ready {
		m.viewport = viewport.New(viewportWidth, viewportHeight)
		m.ready = true
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
	} else {
		m.viewport.Width = viewportWidth
		m.viewport.Height = viewportHeight
		m.viewport.SetContent(m.renderMessages())
	}

	m.textarea.SetWidth(viewportWidth - styles.TextAreaStyle.GetHorizontalPadding() - styles.TextAreaStyle.GetHorizontalBorderSize())
}
