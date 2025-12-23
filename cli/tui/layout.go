package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"

	"github.com/malonaz/sgpt/cli/tui/styles"
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

// scrollToNavigatedMessage scrolls the viewport to show the currently navigated message,
// but only if it's not already fully visible.
func (m *Model) scrollToNavigatedMessage() {
	if m.navigationMessageIndex < 0 || m.navigationMessageIndex >= len(m.messageViewportOffsets) {
		return
	}

	startLine := m.messageViewportOffsets[m.navigationMessageIndex]

	// Calculate end line
	var endLine int
	if m.navigationMessageIndex+1 < len(m.messageViewportOffsets) {
		endLine = m.messageViewportOffsets[m.navigationMessageIndex+1] - 1
	} else {
		endLine = m.viewport.TotalLineCount()
	}

	// Check if fully visible
	viewportTop := m.viewport.YOffset
	viewportBottom := viewportTop + m.viewport.Height

	if startLine >= viewportTop && endLine < viewportBottom {
		return // Already fully visible
	}

	m.viewport.SetYOffset(startLine)
}

// scrollToNavigatedBlock scrolls the viewport to show the currently navigated block,
// but only if it's not already fully visible.
func (m *Model) scrollToNavigatedBlock() {
	if m.navigationMessageIndex < 0 || m.navigationMessageIndex >= len(m.blockViewportOffsets) {
		return
	}
	blockOffsets := m.blockViewportOffsets[m.navigationMessageIndex]
	if m.navigationBlockIndex < 0 || m.navigationBlockIndex >= len(blockOffsets) {
		return
	}

	startLine := blockOffsets[m.navigationBlockIndex]

	// Calculate end line
	var endLine int
	if m.navigationBlockIndex+1 < len(blockOffsets) {
		endLine = blockOffsets[m.navigationBlockIndex+1] - 1
	} else if m.navigationMessageIndex+1 < len(m.messageViewportOffsets) {
		endLine = m.messageViewportOffsets[m.navigationMessageIndex+1] - 1
	} else {
		endLine = m.viewport.TotalLineCount()
	}

	// Check if fully visible
	viewportTop := m.viewport.YOffset
	viewportBottom := viewportTop + m.viewport.Height

	if startLine >= viewportTop && endLine < viewportBottom {
		return // Already fully visible
	}

	m.viewport.SetYOffset(startLine)
}

// recalculateLayout adjusts viewport and textarea dimensions based on current state.
func (m *Model) recalculateLayout() {
	if m.width == 0 || m.height == 0 {
		return
	}

	// Compute viewport height.
	m.renderTitle() // To compute the number of lines.
	viewportHeight := m.height - m.titleHeight
	if !m.streaming {
		viewportHeight -= m.textarea.Height() + styles.TextAreaStyle.GetVerticalFrameSize()
	}
	if viewportHeight < styles.MinViewportHeight {
		viewportHeight = styles.MinViewportHeight
	}

	viewportWidth := m.width
	contentWidth := viewportWidth
	// Account for message frame size and block indicator width
	rendererWidth := contentWidth - styles.MessageHorizontalFrameSize() - styles.BlockIndicatorWidth
	m.renderer.SetWidth(rendererWidth)

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
