package chat

import (
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"

	"github.com/malonaz/sgpt/internal/markdown"
)

func (m *Model) toTop() bool {
	if len(m.chat.Metadata.Messages) == 0 {
		return false
	}
	if m.navigationMessageIndex == 0 && m.navigationBlockIndex == 0 {
		return false
	}
	m.navigationMessageIndex = 0
	m.navigationBlockIndex = 0
	return true
}

func (m *Model) toBottom() bool {
	messageCount := m.totalMessageCount()
	if messageCount == 0 {
		return false
	}
	lastMsgIndex := messageCount - 1
	lastBlockIndex := m.blockCountForMessage(lastMsgIndex) - 1
	if lastBlockIndex < 0 {
		lastBlockIndex = 0
	}
	if m.navigationMessageIndex == lastMsgIndex && m.navigationBlockIndex == lastBlockIndex {
		return false
	}
	m.navigationMessageIndex = lastMsgIndex
	m.navigationBlockIndex = lastBlockIndex
	return true
}

func (m *Model) toPreviousMessage() bool {
	messageCount := m.totalMessageCount()
	if messageCount == 0 {
		return false
	}
	if m.navigationMessageIndex == -1 {
		m.navigationMessageIndex = messageCount - 1
		m.navigationBlockIndex = m.lastBlockIndex()
		return true
	}
	if m.navigationMessageIndex == 0 {
		return false
	}
	m.navigationMessageIndex--
	m.navigationBlockIndex = m.lastBlockIndex()
	return true
}

func (m *Model) toNextMessage() bool {
	messageCount := m.totalMessageCount()
	if messageCount == 0 || m.navigationMessageIndex == -1 {
		return false
	}
	if m.navigationMessageIndex >= messageCount-1 {
		if m.streaming {
			m.viewport.GotoBottom()
		}
		return false
	}
	m.navigationMessageIndex++
	m.navigationBlockIndex = 0
	return true
}

func (m *Model) toPreviousBlock() bool {
	if m.totalMessageCount() == 0 {
		return false
	}
	if m.navigationMessageIndex == -1 {
		return m.toPreviousMessage()
	}
	if m.navigationBlockIndex == -1 {
		m.navigationBlockIndex = m.lastBlockIndex()
		return true
	}
	if m.navigationBlockIndex > 0 {
		m.navigationBlockIndex--
		return true
	}
	return m.toPreviousMessage()
}

func (m *Model) toNextBlock() bool {
	if m.totalMessageCount() == 0 || m.navigationMessageIndex == -1 {
		return false
	}
	if m.navigationBlockIndex == -1 {
		m.navigationBlockIndex = 0
		return true
	}
	blockCount := m.blockCountForMessage(m.navigationMessageIndex)
	if m.navigationBlockIndex < blockCount-1 {
		m.navigationBlockIndex++
		return true
	}
	return m.toNextMessage()
}

func (m *Model) lastBlockIndex() int {
	count := m.blockCountForMessage(m.navigationMessageIndex)
	if count == 0 {
		return 0
	}
	return count - 1
}

func (m *Model) totalMessageCount() int {
	count := len(m.chat.Metadata.Messages)
	if m.pendingUserMessage != nil {
		count++
	}
	if m.streamingMessage != nil {
		count++
	}
	return count
}

func (m *Model) blockCountForMessage(displayIndex int) int {
	if displayIndex >= 0 && displayIndex < len(m.markdownBlockCounts) {
		return m.markdownBlockCounts[displayIndex]
	}
	return m.countMarkdownBlocksForMessage(displayIndex)
}

func (m *Model) countMarkdownBlocksForMessage(displayIndex int) int {
	persistedCount := len(m.chat.Metadata.Messages)
	var message *aipb.Message

	if displayIndex < persistedCount {
		message = m.chat.Metadata.Messages[displayIndex].Message
	} else {
		offset := persistedCount
		if m.pendingUserMessage != nil {
			if displayIndex == offset {
				message = m.pendingUserMessage
			}
			offset++
		}
		if m.streamingMessage != nil && displayIndex == offset {
			message = m.streamingMessage
		}
	}

	if message == nil {
		return 0
	}

	count := 0
	switch message.Role {
	case aipb.Role_ROLE_USER:
		for _, block := range message.GetBlocks() {
			if text := block.GetText(); text != "" {
				count += len(markdown.ParseBlocks(text))
			}
		}
	case aipb.Role_ROLE_ASSISTANT:
		for _, block := range ai.FilterBlocks(message.GetBlocks(), ai.BlockTypeThought) {
			count += len(markdown.ParseBlocks(block.GetThought()))
		}
		for _, block := range ai.FilterBlocks(message.GetBlocks(), ai.BlockTypeText) {
			count += len(markdown.ParseBlocks(block.GetText()))
		}
		count += len(ai.FilterBlocks(message.GetBlocks(), ai.BlockTypeToolCall))
	case aipb.Role_ROLE_TOOL:
		for _, block := range message.GetBlocks() {
			if block.GetToolResult() != nil {
				count++
			}
		}
	case aipb.Role_ROLE_SYSTEM:
		count = 1
	}
	return count
}
