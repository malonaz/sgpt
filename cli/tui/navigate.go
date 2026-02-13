package tui

// toTop navigates to the first block of the first message.
// Returns true if navigation occurred and re-render is needed.
func (m *Model) toTop() bool {
	if len(m.runtimeMessages) == 0 {
		return false
	}

	// Already at top
	if m.navigationMessageIndex == 0 && m.navigationBlockIndex == 0 {
		return false
	}

	m.navigationMessageIndex = 0
	m.navigationBlockIndex = 0
	return true
}

// toBottom navigates to the last block of the last message.
// Returns true if navigation occurred and re-render is needed.
func (m *Model) toBottom() bool {
	if len(m.runtimeMessages) == 0 {
		return false
	}

	lastMsgIndex := len(m.runtimeMessages) - 1
	lastBlockIndex := len(m.runtimeMessages[lastMsgIndex].Blocks) - 1
	if lastBlockIndex < 0 {
		lastBlockIndex = 0
	}

	// Already at bottom
	if m.navigationMessageIndex == lastMsgIndex && m.navigationBlockIndex == lastBlockIndex {
		return false
	}

	m.navigationMessageIndex = lastMsgIndex
	m.navigationBlockIndex = lastBlockIndex
	return true
}

// toPreviousMessage navigates to the previous message.
// Sets block index to the last block of the target message.
// Returns true if navigation occurred and re-render is needed.
func (m *Model) toPreviousMessage() bool {
	if len(m.runtimeMessages) == 0 {
		return false
	}

	// Initialize at last message if not navigating
	if m.navigationMessageIndex == -1 {
		m.navigationMessageIndex = len(m.runtimeMessages) - 1
		m.navigationBlockIndex = m.lastBlockIndex()
		return true
	}

	// Already at first message
	if m.navigationMessageIndex == 0 {
		return false
	}

	m.navigationMessageIndex--
	m.navigationBlockIndex = m.lastBlockIndex()
	return true
}

// toNextMessage navigates to the next message.
// Sets block index to the first block of the target message.
// Returns true if navigation occurred and re-render is needed.
func (m *Model) toNextMessage() bool {
	if len(m.runtimeMessages) == 0 {
		return false
	}

	// Can't go "next" if not navigating yet
	if m.navigationMessageIndex == -1 {
		return false
	}

	// Already at last message
	if m.navigationMessageIndex >= len(m.runtimeMessages)-1 {
		return false
	}

	m.navigationMessageIndex++
	m.navigationBlockIndex = 0
	return true
}

// toPreviousBlock navigates to the previous block within the current message,
// or to the last block of the previous message if at the first block.
// When in select-all mode (blockIndex == -1), re-enters block mode at the last block.
// Returns true if navigation occurred and re-render is needed.
func (m *Model) toPreviousBlock() bool {
	if len(m.runtimeMessages) == 0 {
		return false
	}

	// Initialize navigation at last message's last block
	if m.navigationMessageIndex == -1 {
		return m.toPreviousMessage()
	}

	// Exit select-all mode by entering block mode at last block
	if m.navigationBlockIndex == -1 {
		m.navigationBlockIndex = m.lastBlockIndex()
		return true
	}

	// Move to previous block in current message
	if m.navigationBlockIndex > 0 {
		m.navigationBlockIndex--
		return true
	}

	// At first block - move to previous message's last block
	return m.toPreviousMessage()
}

// toNextBlock navigates to the next block within the current message,
// or to the first block of the next message if at the last block.
// When in select-all mode (blockIndex == -1), re-enters block mode at the first block.
// Returns true if navigation occurred and re-render is needed.
func (m *Model) toNextBlock() bool {
	if len(m.runtimeMessages) == 0 {
		return false
	}

	// Can't navigate if not in navigation mode
	if m.navigationMessageIndex == -1 {
		return false
	}

	// Exit select-all mode by entering block mode at first block
	if m.navigationBlockIndex == -1 {
		m.navigationBlockIndex = 0
		return true
	}

	blocks := m.runtimeMessages[m.navigationMessageIndex].Blocks

	// Move to next block in current message
	if m.navigationBlockIndex < len(blocks)-1 {
		m.navigationBlockIndex++
		return true
	}

	// At last block - move to next message's first block
	return m.toNextMessage()
}

// lastBlockIndex returns the last valid block index for the current message.
// Returns 0 if there are no blocks (ensuring a valid index).
func (m *Model) lastBlockIndex() int {
	if m.navigationMessageIndex < 0 || m.navigationMessageIndex >= len(m.runtimeMessages) {
		return 0
	}
	blocks := m.runtimeMessages[m.navigationMessageIndex].Blocks
	if len(blocks) == 0 {
		return 0
	}
	return len(blocks) - 1
}
