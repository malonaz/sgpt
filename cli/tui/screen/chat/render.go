package chat

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"
	"github.com/malonaz/core/go/pbutil"

	"github.com/malonaz/sgpt/cli/tui/styles"
	chatpb "github.com/malonaz/sgpt/genproto/chat/v1"
	"github.com/malonaz/sgpt/internal/markdown"
)

func (m *Model) renderTitleBar() string {
	rendered := styles.TitleStyle.Width(m.width).Render(m.title)
	m.titleHeight = lipgloss.Height(rendered)
	return rendered
}

func (m *Model) renderMessages() string {
	currentLine := 0
	var b strings.Builder
	writeString := func(s string) {
		b.WriteString(s)
		currentLine += strings.Count(s, "\n")
	}

	if len(m.injectedFiles) > 0 {
		for i, f := range m.injectedFiles {
			line := styles.FileStyle.Width(m.viewport.Width()).Render(fmt.Sprintf("📎 File #%d: %s", i+1, f))
			writeString(line)
			writeString("\n")
		}
		writeString("\n")
	}

	allMessages := m.chat.Metadata.Messages
	m.messageViewportOffsets = make([]int, 0, len(allMessages)+2)
	m.blockViewportOffsets = make([][]int, 0, len(allMessages)+2)
	m.markdownBlockCounts = make([]int, 0, len(allMessages)+2)

	for i, chatMessage := range allMessages {
		if i > 0 {
			writeString("\n\n")
		}
		m.messageViewportOffsets = append(m.messageViewportOffsets, currentLine)
		blockOffsets, mdBlockCount := m.renderChatMessage(&b, &currentLine, i, chatMessage, true)
		m.blockViewportOffsets = append(m.blockViewportOffsets, blockOffsets)
		m.markdownBlockCounts = append(m.markdownBlockCounts, mdBlockCount)

		if chatMessage.Error != nil {
			writeString(styles.MessageErrorStyle.Render("\nError: " + chatMessage.Error.GetMessage()))
		}
	}

	displayIndex := len(allMessages)

	if m.pendingUserMessage != nil {
		if displayIndex > 0 {
			writeString("\n\n")
		}
		m.messageViewportOffsets = append(m.messageViewportOffsets, currentLine)
		blockOffsets, mdBlockCount := m.renderUserMessage(&b, &currentLine, displayIndex, m.pendingUserMessage, true)
		m.blockViewportOffsets = append(m.blockViewportOffsets, blockOffsets)
		m.markdownBlockCounts = append(m.markdownBlockCounts, mdBlockCount)
		displayIndex++
	}

	if m.streamingMessage != nil {
		if displayIndex > 0 {
			writeString("\n\n")
		}
		m.messageViewportOffsets = append(m.messageViewportOffsets, currentLine)
		blockOffsets, mdBlockCount := m.renderAIMessage(&b, &currentLine, displayIndex, m.streamingMessage, false)
		m.blockViewportOffsets = append(m.blockViewportOffsets, blockOffsets)
		m.markdownBlockCounts = append(m.markdownBlockCounts, mdBlockCount)
	}

	if m.streamError != nil {
		writeString(styles.MessageErrorStyle.Render("\nError: " + m.streamError.Error()))
	}

	return b.String()
}

func (m *Model) renderChatMessage(b *strings.Builder, currentLine *int, displayIndex int, chatMessage *chatpb.Message, finalized bool) ([]int, int) {
	aiMessage := chatMessage.Message
	if aiMessage == nil {
		return nil, 0
	}

	switch aiMessage.Role {
	case aipb.Role_ROLE_USER:
		return m.renderUserMessage(b, currentLine, displayIndex, aiMessage, finalized)
	case aipb.Role_ROLE_ASSISTANT:
		return m.renderAIMessage(b, currentLine, displayIndex, aiMessage, finalized)
	case aipb.Role_ROLE_TOOL:
		return m.renderToolMessage(b, currentLine, displayIndex, aiMessage)
	case aipb.Role_ROLE_SYSTEM:
		return m.renderSystemMessage(b, currentLine, displayIndex, aiMessage)
	}
	return nil, 0
}

func (m *Model) renderUserMessage(b *strings.Builder, currentLine *int, displayIndex int, message *aipb.Message, finalized bool) ([]int, int) {
	var blockContent strings.Builder
	var blockOffsets []int
	mdBlockIndex := 0
	for bi, block := range message.Blocks {
		text := block.GetText()
		mdBlocks := markdown.ParseBlocks(text)
		for mbi, mdBlock := range mdBlocks {
			if mdBlockIndex > 0 {
				blockContent.WriteString("\n")
			}
			blockOffsets = append(blockOffsets, *currentLine+strings.Count(blockContent.String(), "\n"))
			rendered := m.renderer.ToMarkdown(displayIndex*1000+bi*100+mbi, finalized, mdBlock)
			blockContent.WriteString(m.renderBlockWithIndicator(rendered, displayIndex, mdBlockIndex))
			mdBlockIndex++
		}
	}
	style := m.getMessageStyle(styles.UserMessageStyle, displayIndex)
	rendered := style.Render(blockContent.String())
	b.WriteString(rendered)
	*currentLine += strings.Count(rendered, "\n")
	return blockOffsets, mdBlockIndex
}

func (m *Model) renderAIMessage(b *strings.Builder, currentLine *int, displayIndex int, message *aipb.Message, finalized bool) ([]int, int) {
	var blockOffsets []int
	mdBlockIndex := 0

	thoughtBlocks := ai.FilterBlocks(message.GetBlocks(), ai.BlockTypeThought)
	textBlocks := ai.FilterBlocks(message.GetBlocks(), ai.BlockTypeText)
	toolCallBlocks := ai.FilterBlocks(message.GetBlocks(), ai.BlockTypeToolCall)

	if len(thoughtBlocks) > 0 {
		var thoughtContent strings.Builder
		for bi, block := range thoughtBlocks {
			mdBlocks := markdown.ParseBlocks(block.GetThought())
			for mbi, mdBlock := range mdBlocks {
				if mdBlockIndex > 0 {
					thoughtContent.WriteString("\n")
				}
				blockOffsets = append(blockOffsets, *currentLine+strings.Count(thoughtContent.String(), "\n"))
				rendered := m.renderer.ToMarkdown(displayIndex*1000+bi*100+mbi, finalized, mdBlock)
				thoughtContent.WriteString(m.renderBlockWithIndicator(rendered, displayIndex, mdBlockIndex))
				mdBlockIndex++
			}
		}
		style := m.getMessageStyle(styles.AIThoughtStyle, displayIndex)
		rendered := style.Render(thoughtContent.String())
		b.WriteString(rendered)
		*currentLine += strings.Count(rendered, "\n")
	}

	if len(textBlocks) > 0 {
		if len(thoughtBlocks) > 0 {
			b.WriteString("\n")
			*currentLine++
		}
		var textContent strings.Builder
		for bi, block := range textBlocks {
			mdBlocks := markdown.ParseBlocks(block.GetText())
			for mbi, mdBlock := range mdBlocks {
				if mdBlockIndex > 0 {
					textContent.WriteString("\n")
				}
				blockOffsets = append(blockOffsets, *currentLine+strings.Count(textContent.String(), "\n"))
				rendered := m.renderer.ToMarkdown(displayIndex*1000+500+bi*100+mbi, finalized, mdBlock)
				textContent.WriteString(m.renderBlockWithIndicator(rendered, displayIndex, mdBlockIndex))
				mdBlockIndex++
			}
		}
		style := m.getMessageStyle(styles.AIMessageStyle, displayIndex)
		rendered := style.Render(textContent.String())
		b.WriteString(rendered)
		*currentLine += strings.Count(rendered, "\n")
	}

	for _, block := range toolCallBlocks {
		toolCall := block.GetToolCall()
		b.WriteString("\n")
		*currentLine++
		blockOffsets = append(blockOffsets, *currentLine)
		b.WriteString(styles.ToolLabelStyle.Render(fmt.Sprintf("🔧 Tool: %s", toolCall.Name)))
		b.WriteString("\n")
		*currentLine++
		bytes, _ := pbutil.JSONMarshalPretty(toolCall.Arguments)
		rendered := styles.ToolCallStyle.Render(string(bytes))
		b.WriteString(rendered)
		*currentLine += strings.Count(rendered, "\n")
		mdBlockIndex++
	}

	return blockOffsets, mdBlockIndex
}

func (m *Model) renderToolMessage(b *strings.Builder, currentLine *int, displayIndex int, message *aipb.Message) ([]int, int) {
	var blockOffsets []int
	mdBlockIndex := 0
	for _, block := range message.Blocks {
		toolResult := block.GetToolResult()
		if toolResult == nil {
			continue
		}
		blockOffsets = append(blockOffsets, *currentLine)
		b.WriteString(styles.ToolLabelStyle.Render("⚡ Tool Result:"))
		b.WriteString("\n")
		*currentLine++
		content, _ := ai.ParseToolResult(toolResult)
		rendered := styles.ToolResultStyle.Render(content)
		b.WriteString(rendered)
		*currentLine += strings.Count(rendered, "\n")
		mdBlockIndex++
	}
	return blockOffsets, mdBlockIndex
}

func (m *Model) renderSystemMessage(b *strings.Builder, currentLine *int, displayIndex int, message *aipb.Message) ([]int, int) {
	blockOffsets := []int{*currentLine}
	for _, block := range message.Blocks {
		text := block.GetText()
		rendered := styles.SystemStyle.Render(fmt.Sprintf("System: %s", styles.Truncate(text, styles.TruncateLength)))
		b.WriteString(rendered)
		*currentLine += strings.Count(rendered, "\n")
	}
	return blockOffsets, 1
}

func (m *Model) getMessageStyle(style lipgloss.Style, displayIndex int) lipgloss.Style {
	style = style.Width(m.width - styles.MessageHorizontalFrameSize())
	if m.focusedComponent != FocusViewport {
		return style
	}
	fg := styles.MessageUnselectedColor
	if displayIndex == m.navigationMessageIndex {
		fg = styles.MessageSelectedColor
	}
	return style.BorderForeground(fg)
}

func (m *Model) getBlockIndicatorStyle(messageIndex, blockIndex int) lipgloss.Style {
	if m.focusedComponent != FocusViewport {
		return styles.BlockIndicatorStyle
	}
	if m.navigationMessageIndex != messageIndex {
		return styles.BlockIndicatorStyle
	}
	if m.navigationBlockIndex == -1 || m.navigationBlockIndex == blockIndex {
		return styles.BlockIndicatorSelectedStyle
	}
	return styles.BlockIndicatorStyle
}

func (m *Model) renderBlockWithIndicator(content string, messageIndex, blockIndex int) string {
	indicatorStyle := m.getBlockIndicatorStyle(messageIndex, blockIndex)
	lines := strings.Split(content, "\n")
	var result strings.Builder
	for i, line := range lines {
		if i > 0 {
			result.WriteString("\n")
		}
		result.WriteString(indicatorStyle.Render(styles.BlockIndicatorChar))
		result.WriteString(" ")
		result.WriteString(line)
	}
	return result.String()
}

func (m *Model) getSelectedContent() (string, string) {
	messages := m.chat.Metadata.Messages
	if m.navigationMessageIndex < 0 || m.navigationMessageIndex >= len(messages) {
		return "", ""
	}

	aiMessage := messages[m.navigationMessageIndex].Message
	if aiMessage == nil {
		return "", ""
	}

	allMdBlocks := m.collectMarkdownBlocks(aiMessage)

	if m.navigationBlockIndex == -1 {
		var content strings.Builder
		for _, mdBlock := range allMdBlocks {
			content.WriteString(mdBlock.Content())
		}
		return content.String(), ""
	}

	if m.navigationBlockIndex >= 0 && m.navigationBlockIndex < len(allMdBlocks) {
		mdBlock := allMdBlocks[m.navigationBlockIndex]
		return mdBlock.Content(), mdBlock.Extension()
	}
	return "", ""
}

func (m *Model) collectMarkdownBlocks(message *aipb.Message) []markdown.Block {
	var result []markdown.Block

	switch message.Role {
	case aipb.Role_ROLE_USER:
		for _, block := range message.GetBlocks() {
			if text := block.GetText(); text != "" {
				result = append(result, markdown.ParseBlocks(text)...)
			}
		}
	case aipb.Role_ROLE_ASSISTANT:
		for _, block := range ai.FilterBlocks(message.GetBlocks(), ai.BlockTypeThought) {
			result = append(result, markdown.ParseBlocks(block.GetThought())...)
		}
		for _, block := range ai.FilterBlocks(message.GetBlocks(), ai.BlockTypeText) {
			result = append(result, markdown.ParseBlocks(block.GetText())...)
		}
	}

	return result
}
