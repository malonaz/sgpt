package chat

import (
	"strings"

	"charm.land/bubbles/v2/viewport"

	"github.com/malonaz/sgpt/cli/tui/styles"
)

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
	if oldHeight == newHeight {
		return
	}

	m.textarea.SetHeight(newHeight)
	heightDiff := newHeight - oldHeight
	m.recalculateLayout()
	if heightDiff != 0 && m.ready {
		m.viewport.ScrollDown(heightDiff)
	}
}

func (m *Model) scrollToNavigatedMessage() {
	if m.navigationMessageIndex < 0 || m.navigationMessageIndex >= len(m.messageViewportOffsets) {
		return
	}
	startLine := m.messageViewportOffsets[m.navigationMessageIndex]
	var endLine int
	if m.navigationMessageIndex+1 < len(m.messageViewportOffsets) {
		endLine = m.messageViewportOffsets[m.navigationMessageIndex+1] - 1
	} else {
		endLine = m.viewport.TotalLineCount()
	}
	viewportTop := m.viewport.YOffset()
	viewportBottom := viewportTop + m.viewport.Height()
	if startLine >= viewportTop && endLine < viewportBottom {
		return
	}
	m.viewport.SetYOffset(startLine)
}

func (m *Model) scrollToNavigatedBlock() {
	if m.navigationMessageIndex < 0 || m.navigationMessageIndex >= len(m.blockViewportOffsets) {
		return
	}
	blockOffsets := m.blockViewportOffsets[m.navigationMessageIndex]
	if m.navigationBlockIndex < 0 || m.navigationBlockIndex >= len(blockOffsets) {
		return
	}
	startLine := blockOffsets[m.navigationBlockIndex]
	var endLine int
	if m.navigationBlockIndex+1 < len(blockOffsets) {
		endLine = blockOffsets[m.navigationBlockIndex+1] - 1
	} else if m.navigationMessageIndex+1 < len(m.messageViewportOffsets) {
		endLine = m.messageViewportOffsets[m.navigationMessageIndex+1] - 1
	} else {
		endLine = m.viewport.TotalLineCount()
	}
	viewportTop := m.viewport.YOffset()
	viewportBottom := viewportTop + m.viewport.Height()
	if startLine >= viewportTop && endLine < viewportBottom {
		return
	}
	m.viewport.SetYOffset(startLine)
}

func (m *Model) recalculateLayout() {
	if m.width == 0 || m.height == 0 {
		return
	}

	m.renderTitleBar()
	viewportHeight := m.height - m.titleHeight
	if !m.streaming {
		viewportHeight -= m.textarea.Height() + styles.TextAreaStyle.GetVerticalFrameSize()
	}
	if viewportHeight < styles.MinViewportHeight {
		viewportHeight = styles.MinViewportHeight
	}

	rendererWidth := m.width - styles.MessageHorizontalFrameSize() - styles.BlockIndicatorWidth
	m.renderer.SetWidth(rendererWidth)

	if !m.ready {
		m.viewport = viewport.New(
			viewport.WithWidth(m.width),
			viewport.WithHeight(viewportHeight),
		)
		m.ready = true
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
	} else {
		m.viewport.SetWidth(m.width)
		m.viewport.SetHeight(viewportHeight)
		m.viewport.SetContent(m.renderMessages())
	}

	m.textarea.SetWidth(m.width - styles.TextAreaStyle.GetHorizontalPadding() - styles.TextAreaStyle.GetHorizontalBorderSize())
}
