package tui

import (
	"strings"

	"charm.land/bubbles/v2/viewport"

	"github.com/malonaz/sgpt/cli/tui/styles"
)

// adjustTextareaHeight resizes the textarea based on the number of content lines,
// clamped between MinTextareaHeight and MaxTextareaHeight. When the height changes,
// it triggers a layout recalculation and scrolls the viewport down by the height
// difference so the user's view stays anchored.
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

		// Scroll viewport to compensate for the textarea height change,
		// keeping the visible content position stable.
		if heightDiff != 0 && m.ready {
			m.viewport.ScrollDown(heightDiff)
		}
	}
}

// scrollToNavigatedMessage scrolls the viewport to show the currently navigated message,
// but only if it's not already fully visible. Uses messageViewportOffsets to determine
// the line range of the target message.
func (m *Model) scrollToNavigatedMessage() {
	if m.navigationMessageIndex < 0 || m.navigationMessageIndex >= len(m.messageViewportOffsets) {
		return
	}

	startLine := m.messageViewportOffsets[m.navigationMessageIndex]

	// Calculate the end line of this message by looking at the next message's offset,
	// or falling back to the total line count if this is the last message.
	var endLine int
	if m.navigationMessageIndex+1 < len(m.messageViewportOffsets) {
		endLine = m.messageViewportOffsets[m.navigationMessageIndex+1] - 1
	} else {
		endLine = m.viewport.TotalLineCount()
	}

	// Determine the currently visible line range in the viewport.
	viewportTop := m.viewport.YOffset()
	viewportBottom := viewportTop + m.viewport.Height()

	// Skip scrolling if the entire message is already within the visible area.
	if startLine >= viewportTop && endLine < viewportBottom {
		return
	}

	m.viewport.SetYOffset(startLine)
}

// scrollToNavigatedBlock scrolls the viewport to show the currently navigated block
// within the current message, but only if it's not already fully visible.
// Uses blockViewportOffsets (nested by message index, then block index) to determine
// the line range of the target block.
func (m *Model) scrollToNavigatedBlock() {
	if m.navigationMessageIndex < 0 || m.navigationMessageIndex >= len(m.blockViewportOffsets) {
		return
	}
	blockOffsets := m.blockViewportOffsets[m.navigationMessageIndex]
	if m.navigationBlockIndex < 0 || m.navigationBlockIndex >= len(blockOffsets) {
		return
	}

	startLine := blockOffsets[m.navigationBlockIndex]

	// Calculate end line: use the next block's offset, or the next message's offset,
	// or the total line count as a fallback.
	var endLine int
	if m.navigationBlockIndex+1 < len(blockOffsets) {
		endLine = blockOffsets[m.navigationBlockIndex+1] - 1
	} else if m.navigationMessageIndex+1 < len(m.messageViewportOffsets) {
		endLine = m.messageViewportOffsets[m.navigationMessageIndex+1] - 1
	} else {
		endLine = m.viewport.TotalLineCount()
	}

	// Determine the currently visible line range.
	viewportTop := m.viewport.YOffset()
	viewportBottom := viewportTop + m.viewport.Height()

	// Skip scrolling if the entire block is already within the visible area.
	if startLine >= viewportTop && endLine < viewportBottom {
		return
	}

	m.viewport.SetYOffset(startLine)
}

// recalculateLayout recomputes the viewport and textarea dimensions based on the
// current terminal size and streaming state. It accounts for the title bar height
// and textarea height (when not streaming) to determine the available viewport area.
// Also updates the markdown renderer width to match the content area minus frame
// and block indicator widths.
func (m *Model) recalculateLayout() {
	if m.width == 0 || m.height == 0 {
		return
	}

	// Rerender the title bar to get its current height.
	m.renderTitle()
	viewportHeight := m.height - m.titleHeight
	if !m.streaming {
		// Subtract the textarea and its border/padding when the input area is visible.
		viewportHeight -= m.textarea.Height() + styles.TextAreaStyle.GetVerticalFrameSize()
	}
	if viewportHeight < styles.MinViewportHeight {
		viewportHeight = styles.MinViewportHeight
	}

	viewportWidth := m.width
	contentWidth := viewportWidth
	// Subtract the message border/padding and the block indicator column from
	// the available width so the markdown renderer wraps correctly.
	rendererWidth := contentWidth - styles.MessageHorizontalFrameSize() - styles.BlockIndicatorWidth
	m.renderer.SetWidth(rendererWidth)

	if !m.ready {
		// First layout pass: create the viewport with the computed dimensions.
		m.viewport = viewport.New(
			viewport.WithWidth(viewportWidth),
			viewport.WithHeight(viewportHeight),
		)
		m.ready = true
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
	} else {
		// Subsequent layout passes: resize the existing viewport.
		m.viewport.SetWidth(viewportWidth)
		m.viewport.SetHeight(viewportHeight)
		m.viewport.SetContent(m.renderMessages())
	}

	m.textarea.SetWidth(viewportWidth - styles.TextAreaStyle.GetHorizontalPadding() - styles.TextAreaStyle.GetHorizontalBorderSize())
}
