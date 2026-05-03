package widget

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"
	"github.com/malonaz/core/go/pbutil"
	"golang.design/x/clipboard"

	"github.com/malonaz/sgpt/cli/tui/styles"
	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/markdown"
	"github.com/malonaz/sgpt/internal/tools"
)

const maxToolDisplayLines = 15

var (
	keyViewportToTop       = key.NewBinding(key.WithKeys("alt+<"))
	keyViewportToBottom    = key.NewBinding(key.WithKeys("alt+>"))
	keyViewportPrevMessage = key.NewBinding(key.WithKeys("alt+{"))
	keyViewportNextMessage = key.NewBinding(key.WithKeys("alt+}"))
	keyViewportPrevBlock   = key.NewBinding(key.WithKeys("alt+["))
	keyViewportNextBlock   = key.NewBinding(key.WithKeys("alt+]"))
	keyViewportSelectAll   = key.NewBinding(key.WithKeys("alt+a"))
	keyViewportOpenAll     = key.NewBinding(key.WithKeys("alt+shift+a"))
	keyViewportScrollUp    = key.NewBinding(key.WithKeys("ctrl+p"))
	keyViewportScrollDown  = key.NewBinding(key.WithKeys("ctrl+n"))
	keyViewportCopy        = key.NewBinding(key.WithKeys("alt+w"))
	keyViewportOpenEditor  = key.NewBinding(key.WithKeys("ctrl+o"))
)

// MessagesData is the data the Messages widget renders from.
type MessagesData struct {
	ChatMessages     []*sgptpb.Message
	StreamingMessage *aipb.Message
	StreamError      error
	InjectedFiles    []string
}

// Messages renders a scrollable message history with block-level navigation.
type Messages struct {
	data     MessagesData
	viewport viewport.Model
	renderer *markdown.Renderer
	width    int
	height   int
	ready    bool
	focused  bool

	navMessageIndex int
	navBlockIndex   int

	messageOffsets      []int
	blockOffsets        [][]int
	markdownBlockCounts []int
}

func NewMessages() *Messages {
	renderer, _ := markdown.NewRenderer(styles.DefaultTextareaWidth)
	return &Messages{
		renderer:        renderer,
		navMessageIndex: -1,
		navBlockIndex:   -1,
	}
}

func (m *Messages) SetFocused(focused bool) {
	m.focused = focused
}

func (m *Messages) IsFocused() bool {
	return m.focused
}

func (m *Messages) SetSize(width, height int) {
	m.width = width
	m.height = height

	rendererWidth := width - styles.MessageHorizontalFrameSize() - styles.BlockIndicatorWidth
	m.renderer.SetWidth(rendererWidth)

	if !m.ready {
		m.viewport = viewport.New(
			viewport.WithWidth(width),
			viewport.WithHeight(height),
		)
		m.ready = true
	} else {
		m.viewport.SetWidth(width)
		m.viewport.SetHeight(height)
	}
	m.rerender()
}

func (m *Messages) SetData(data MessagesData) {
	m.data = data
	m.rerender()
}

func (m *Messages) AtBottom() bool {
	return m.viewport.AtBottom()
}

func (m *Messages) GotoBottom() {
	m.viewport.GotoBottom()
}

func (m *Messages) ScrollDown(n int) {
	m.viewport.ScrollDown(n)
}

func (m *Messages) View() string {
	return styles.ViewportStyle.Render(m.viewport.View())
}

func (m *Messages) HandleKey(msg tea.KeyPressMsg, alertFn func(string)) tea.Cmd {
	switch {
	case key.Matches(msg, keyViewportToTop):
		if m.toTop() {
			m.rerender()
			m.scrollToBlock()
		}
	case key.Matches(msg, keyViewportToBottom):
		if m.toBottom() {
			m.rerender()
			m.scrollToBlock()
		}
	case key.Matches(msg, keyViewportPrevMessage):
		if m.toPreviousMessage() {
			m.rerender()
			m.scrollToMessage()
		}
	case key.Matches(msg, keyViewportNextMessage):
		if m.toNextMessage() {
			m.rerender()
			m.scrollToMessage()
		}
	case key.Matches(msg, keyViewportPrevBlock):
		if m.toPreviousBlock() {
			m.rerender()
			m.scrollToBlock()
		}
	case key.Matches(msg, keyViewportNextBlock):
		if m.toNextBlock() {
			m.rerender()
			m.scrollToBlock()
		}
	case key.Matches(msg, keyViewportSelectAll):
		if m.navMessageIndex != -1 {
			m.navBlockIndex = -1
			m.rerender()
			m.scrollToMessage()
		}
	case key.Matches(msg, keyViewportScrollUp):
		m.viewport.ScrollUp(3)
	case key.Matches(msg, keyViewportScrollDown):
		m.viewport.ScrollDown(3)
	case key.Matches(msg, keyViewportCopy):
		if m.navMessageIndex != -1 {
			content, _ := m.getSelectedContent()
			clipboard.Write(clipboard.FmtText, []byte(content))
			alertFn("Copied to clipboard!")
		}
	case key.Matches(msg, keyViewportOpenAll):
		content := m.fullConversationText()
		return openInEditor(content, "md")
	case key.Matches(msg, keyViewportOpenEditor):
		if m.navMessageIndex != -1 {
			content, ext := m.getSelectedContent()
			return openInEditor(content, ext)
		}
	}
	return nil
}

func (m *Messages) NavigateToBottom() {
	m.toBottom()
}

func (m *Messages) ResetNavigation() {
	m.navMessageIndex = -1
	m.navBlockIndex = -1
}

func (m *Messages) NavMessageIndex() int {
	return m.navMessageIndex
}

// ---- Navigation ----

func (m *Messages) totalMessageCount() int {
	count := len(m.data.ChatMessages)
	if m.data.StreamingMessage != nil {
		count++
	}
	return count
}

func (m *Messages) toTop() bool {
	if m.totalMessageCount() == 0 {
		return false
	}
	if m.navMessageIndex == 0 && m.navBlockIndex == 0 {
		return false
	}
	m.navMessageIndex = 0
	m.navBlockIndex = 0
	return true
}

func (m *Messages) toBottom() bool {
	total := m.totalMessageCount()
	if total == 0 {
		return false
	}
	lastIdx := total - 1
	lastBlock := m.blockCountForMessage(lastIdx) - 1
	if lastBlock < 0 {
		lastBlock = 0
	}
	if m.navMessageIndex == lastIdx && m.navBlockIndex == lastBlock {
		return false
	}
	m.navMessageIndex = lastIdx
	m.navBlockIndex = lastBlock
	return true
}

func (m *Messages) toPreviousMessage() bool {
	total := m.totalMessageCount()
	if total == 0 {
		return false
	}
	if m.navMessageIndex == -1 {
		m.navMessageIndex = total - 1
		m.navBlockIndex = m.lastBlockIdx()
		return true
	}
	if m.navMessageIndex == 0 {
		return false
	}
	m.navMessageIndex--
	m.navBlockIndex = m.lastBlockIdx()
	return true
}

func (m *Messages) toNextMessage() bool {
	total := m.totalMessageCount()
	if total == 0 || m.navMessageIndex == -1 {
		return false
	}
	if m.navMessageIndex >= total-1 {
		m.viewport.GotoBottom()
		return false
	}
	m.navMessageIndex++
	m.navBlockIndex = 0
	return true
}

func (m *Messages) toPreviousBlock() bool {
	if m.totalMessageCount() == 0 {
		return false
	}
	if m.navMessageIndex == -1 {
		return m.toPreviousMessage()
	}
	if m.navBlockIndex > 0 {
		m.navBlockIndex--
		return true
	}
	return m.toPreviousMessage()
}

func (m *Messages) toNextBlock() bool {
	if m.totalMessageCount() == 0 || m.navMessageIndex == -1 {
		return false
	}
	blockCount := m.blockCountForMessage(m.navMessageIndex)
	if m.navBlockIndex < blockCount-1 {
		m.navBlockIndex++
		return true
	}
	return m.toNextMessage()
}

func (m *Messages) lastBlockIdx() int {
	count := m.blockCountForMessage(m.navMessageIndex)
	if count == 0 {
		return 0
	}
	return count - 1
}

func (m *Messages) blockCountForMessage(idx int) int {
	if idx >= 0 && idx < len(m.markdownBlockCounts) {
		return m.markdownBlockCounts[idx]
	}
	return m.countBlocks(idx)
}

func (m *Messages) countBlocks(idx int) int {
	msg := m.messageAt(idx)
	if msg == nil {
		return 0
	}
	count := 0
	switch msg.Role {
	case aipb.Role_ROLE_USER:
		for _, block := range msg.GetBlocks() {
			if text := block.GetText(); text != "" {
				count += len(markdown.ParseBlocks(text))
			}
		}
	case aipb.Role_ROLE_ASSISTANT:
		for _, block := range ai.FilterBlocks(msg.GetBlocks(), ai.BlockTypeThought) {
			count += len(markdown.ParseBlocks(block.GetThought()))
		}
		for _, block := range ai.FilterBlocks(msg.GetBlocks(), ai.BlockTypeText) {
			count += len(markdown.ParseBlocks(block.GetText()))
		}
		count += len(ai.FilterBlocks(msg.GetBlocks(), ai.BlockTypeToolCall))
	case aipb.Role_ROLE_TOOL:
		for _, block := range msg.GetBlocks() {
			if block.GetToolResult() != nil {
				count++
			}
		}
	case aipb.Role_ROLE_SYSTEM:
		count = 1
	}
	return count
}

func (m *Messages) messageAt(idx int) *aipb.Message {
	persisted := len(m.data.ChatMessages)
	if idx < persisted {
		return m.data.ChatMessages[idx].GetMessage()
	}
	if m.data.StreamingMessage != nil && idx == persisted {
		return m.data.StreamingMessage
	}
	return nil
}

func (m *Messages) scrollToMessage() {
	if m.navMessageIndex < 0 || m.navMessageIndex >= len(m.messageOffsets) {
		return
	}
	m.viewport.SetYOffset(m.messageOffsets[m.navMessageIndex])
}

func (m *Messages) scrollToBlock() {
	if m.navMessageIndex < 0 || m.navMessageIndex >= len(m.blockOffsets) {
		return
	}
	blockOffsets := m.blockOffsets[m.navMessageIndex]
	if m.navBlockIndex < 0 || m.navBlockIndex >= len(blockOffsets) {
		m.scrollToMessage()
		return
	}
	m.viewport.SetYOffset(blockOffsets[m.navBlockIndex])
}

// ---- Rendering ----

func (m *Messages) rerender() {
	if !m.ready {
		return
	}
	m.viewport.SetContent(m.renderAll())
}

func (m *Messages) renderAll() string {
	currentLine := 0
	var b strings.Builder
	write := func(s string) {
		b.WriteString(s)
		currentLine += strings.Count(s, "\n")
	}

	for i, f := range m.data.InjectedFiles {
		line := styles.FileStyle.Width(m.viewport.Width()).Render(fmt.Sprintf("File #%d: %s", i+1, f))
		write(line)
		write("\n")
	}
	if len(m.data.InjectedFiles) > 0 {
		write("\n")
	}

	allMessages := m.data.ChatMessages
	m.messageOffsets = make([]int, 0, len(allMessages)+2)
	m.blockOffsets = make([][]int, 0, len(allMessages)+2)
	m.markdownBlockCounts = make([]int, 0, len(allMessages)+2)

	for i, chatMessage := range allMessages {
		if i > 0 {
			write("\n")
			if allMessages[i-1].GetMessage().GetRole() == aipb.Role_ROLE_USER {
				write("\n")
			}
		}
		m.messageOffsets = append(m.messageOffsets, currentLine)
		blockOff, mdCount := m.renderChatMessage(&b, &currentLine, i, chatMessage, true)
		m.blockOffsets = append(m.blockOffsets, blockOff)
		m.markdownBlockCounts = append(m.markdownBlockCounts, mdCount)

		if chatMessage.Error != nil {
			write(styles.MessageErrorStyle.Render("\nError: " + chatMessage.Error.GetMessage()))
		}
	}

	displayIndex := len(allMessages)

	if m.data.StreamingMessage != nil {
		if displayIndex > 0 {
			write("\n\n")
		}
		m.messageOffsets = append(m.messageOffsets, currentLine)
		blockOff, mdCount := m.renderAIMessage(&b, &currentLine, displayIndex, m.data.StreamingMessage, false)
		m.blockOffsets = append(m.blockOffsets, blockOff)
		m.markdownBlockCounts = append(m.markdownBlockCounts, mdCount)
	}

	if m.data.StreamError != nil {
		write(styles.MessageErrorStyle.Render("\nError: " + m.data.StreamError.Error()))
	}

	return b.String()
}

func (m *Messages) renderChatMessage(b *strings.Builder, currentLine *int, displayIndex int, chatMessage *sgptpb.Message, finalized bool) ([]int, int) {
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

func (m *Messages) renderUserMessage(b *strings.Builder, currentLine *int, displayIndex int, message *aipb.Message, finalized bool) ([]int, int) {
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
			blockContent.WriteString(m.blockWithIndicator(rendered, displayIndex, mdBlockIndex))
			mdBlockIndex++
		}
	}
	style := m.messageStyle(styles.UserMessageStyle, displayIndex)
	rendered := style.Render(blockContent.String())
	b.WriteString(rendered)
	*currentLine += strings.Count(rendered, "\n")
	return blockOffsets, mdBlockIndex
}

func (m *Messages) renderAIMessage(b *strings.Builder, currentLine *int, displayIndex int, message *aipb.Message, finalized bool) ([]int, int) {
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
				thoughtContent.WriteString(m.blockWithIndicator(rendered, displayIndex, mdBlockIndex))
				mdBlockIndex++
			}
		}
		style := m.messageStyle(styles.AIThoughtStyle, displayIndex)
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
				textContent.WriteString(m.blockWithIndicator(rendered, displayIndex, mdBlockIndex))
				mdBlockIndex++
			}
		}
		style := m.messageStyle(styles.AIMessageStyle, displayIndex)
		rendered := style.Render(textContent.String())
		b.WriteString(rendered)
		*currentLine += strings.Count(rendered, "\n")
	}

	for _, block := range toolCallBlocks {
		toolCall := block.GetToolCall()
		if len(thoughtBlocks) > 0 || len(textBlocks) > 0 || mdBlockIndex > 0 {
			b.WriteString("\n")
			*currentLine++
		}
		blockOffsets = append(blockOffsets, *currentLine)

		var content strings.Builder
		metadata, _ := tools.ParseToolCallMetadata(toolCall)
		displayMessage := ""
		if metadata != nil && metadata.GetDisplayMessage().GetContent() != "" {
			displayMessage = metadata.GetDisplayMessage().GetContent()
		}

		statusSuffix := ""
		switch tools.GetToolCallStatus(toolCall) {
		case tools.ToolCallStatusPending:
			statusSuffix = " [PENDING]"
		case tools.ToolCallStatusRejected:
			statusSuffix = " [REJECTED]"
		}

		if displayMessage != "" {
			rendered := m.renderer.ToMarkdown(displayIndex*1000+900+mdBlockIndex, finalized, markdown.ParseBlocks(fmt.Sprintf("tool: %s%s", displayMessage, statusSuffix))[0])
			content.WriteString(m.blockWithIndicator(rendered, displayIndex, mdBlockIndex))
		} else {
			labelRendered := styles.ToolLabelStyle.Render(fmt.Sprintf("tool: %s%s", toolCall.Name, statusSuffix))
			content.WriteString(m.blockWithIndicator(labelRendered, displayIndex, mdBlockIndex))
			bytes, _ := pbutil.JSONMarshalPretty(toolCall.Arguments)
			body := truncateLines(string(bytes), maxToolDisplayLines)
			content.WriteString("\n")
			content.WriteString(m.blockWithIndicator(styles.ToolCallStyle.Render(body), displayIndex, mdBlockIndex))
		}

		style := m.messageStyle(styles.AIMessageStyle, displayIndex)
		rendered := style.Render(content.String())
		b.WriteString(rendered)
		*currentLine += strings.Count(rendered, "\n")
		mdBlockIndex++
	}

	return blockOffsets, mdBlockIndex
}

func (m *Messages) renderToolMessage(b *strings.Builder, currentLine *int, displayIndex int, message *aipb.Message) ([]int, int) {
	var blockOffsets []int
	mdBlockIndex := 0
	for _, block := range message.Blocks {
		toolResult := block.GetToolResult()
		if toolResult == nil {
			continue
		}
		if mdBlockIndex > 0 {
			b.WriteString("\n")
			*currentLine++
		}
		blockOffsets = append(blockOffsets, *currentLine)

		var content strings.Builder
		metadata, _ := tools.ParseToolResultMetadata(toolResult)
		if metadata != nil && metadata.GetDisplayMessage().GetContent() != "" {
			rendered := m.renderer.ToMarkdown(displayIndex*1000+900+mdBlockIndex, true, markdown.ParseBlocks(fmt.Sprintf("result: %s", metadata.GetDisplayMessage().GetContent()))[0])
			content.WriteString(m.blockWithIndicator(rendered, displayIndex, mdBlockIndex))
		} else {
			labelRendered := styles.ToolLabelStyle.Render(fmt.Sprintf("result: %s", toolResult.GetToolName()))
			content.WriteString(m.blockWithIndicator(labelRendered, displayIndex, mdBlockIndex))
			content.WriteString("\n")
			var body string
			if toolResult.GetError() != nil {
				body = fmt.Sprintf("Error: %s", toolResult.GetError().GetMessage())
			} else if structured := toolResult.GetStructuredContent(); structured != nil {
				bytes, _ := pbutil.JSONMarshalPretty(structured)
				body = string(bytes)
			} else {
				body = toolResult.GetContent()
			}
			body = truncateLines(body, maxToolDisplayLines)
			content.WriteString(m.blockWithIndicator(styles.ToolResultStyle.Render(body), displayIndex, mdBlockIndex))
		}

		style := m.messageStyle(styles.AIMessageStyle, displayIndex)
		rendered := style.Render(content.String())
		b.WriteString(rendered)
		*currentLine += strings.Count(rendered, "\n")
		mdBlockIndex++
	}
	return blockOffsets, mdBlockIndex
}

func (m *Messages) renderSystemMessage(b *strings.Builder, currentLine *int, displayIndex int, message *aipb.Message) ([]int, int) {
	blockOffsets := []int{*currentLine}
	for _, block := range message.Blocks {
		text := block.GetText()
		rendered := styles.SystemStyle.Render(fmt.Sprintf("System: %s", styles.Truncate(text, styles.TruncateLength)))
		b.WriteString(rendered)
		*currentLine += strings.Count(rendered, "\n")
	}
	return blockOffsets, 1
}

func (m *Messages) messageStyle(style lipgloss.Style, displayIndex int) lipgloss.Style {
	style = style.Width(m.width - styles.MessageHorizontalFrameSize())
	if !m.focused {
		return style
	}
	fg := styles.MessageUnselectedColor
	if displayIndex == m.navMessageIndex {
		fg = styles.MessageSelectedColor
	}
	return style.BorderForeground(fg)
}

func (m *Messages) blockIndicatorStyle(messageIndex, blockIndex int) lipgloss.Style {
	if !m.focused {
		return styles.BlockIndicatorStyle
	}
	if m.navMessageIndex != messageIndex {
		return styles.BlockIndicatorStyle
	}
	if m.navBlockIndex == -1 || m.navBlockIndex == blockIndex {
		return styles.BlockIndicatorSelectedStyle
	}
	return styles.BlockIndicatorStyle
}

func (m *Messages) blockWithIndicator(content string, messageIndex, blockIndex int) string {
	indicatorStyle := m.blockIndicatorStyle(messageIndex, blockIndex)
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

func (m *Messages) getSelectedContent() (string, string) {
	if m.navMessageIndex < 0 {
		return "", ""
	}
	msg := m.messageAt(m.navMessageIndex)
	if msg == nil {
		return "", ""
	}

	switch msg.Role {
	case aipb.Role_ROLE_ASSISTANT:
		return m.getSelectedAssistantContent(msg)
	case aipb.Role_ROLE_TOOL:
		return m.getSelectedToolResultContent(msg)
	}

	allMdBlocks := m.collectMarkdownBlocks(msg)
	if m.navBlockIndex == -1 {
		var content strings.Builder
		for _, mdBlock := range allMdBlocks {
			content.WriteString(mdBlock.Content())
		}
		return content.String(), ""
	}
	if m.navBlockIndex >= 0 && m.navBlockIndex < len(allMdBlocks) {
		mdBlock := allMdBlocks[m.navBlockIndex]
		return mdBlock.Content(), mdBlock.Extension()
	}
	return "", ""
}

func (m *Messages) getSelectedAssistantContent(message *aipb.Message) (string, string) {
	allMdBlocks := m.collectMarkdownBlocks(message)
	toolCallBlocks := ai.FilterBlocks(message.GetBlocks(), ai.BlockTypeToolCall)
	mdBlockCount := len(allMdBlocks)

	if m.navBlockIndex == -1 {
		var content strings.Builder
		for _, mdBlock := range allMdBlocks {
			content.WriteString(mdBlock.Content())
		}
		for _, block := range toolCallBlocks {
			toolCall := block.GetToolCall()
			bytes, _ := pbutil.JSONMarshalPretty(toolCall.Arguments)
			content.WriteString(fmt.Sprintf("\n\nTool Call: %s\n%s", toolCall.Name, string(bytes)))
		}
		return content.String(), ""
	}

	if m.navBlockIndex >= 0 && m.navBlockIndex < mdBlockCount {
		mdBlock := allMdBlocks[m.navBlockIndex]
		return mdBlock.Content(), mdBlock.Extension()
	}

	toolCallIndex := m.navBlockIndex - mdBlockCount
	if toolCallIndex >= 0 && toolCallIndex < len(toolCallBlocks) {
		toolCall := toolCallBlocks[toolCallIndex].GetToolCall()
		bytes, _ := pbutil.JSONMarshalPretty(toolCall.Arguments)
		return fmt.Sprintf("Tool Call: %s\n%s", toolCall.Name, string(bytes)), "json"
	}
	return "", ""
}

func (m *Messages) getSelectedToolResultContent(message *aipb.Message) (string, string) {
	var toolResults []*aipb.ToolResult
	for _, block := range message.Blocks {
		if toolResult := block.GetToolResult(); toolResult != nil {
			toolResults = append(toolResults, toolResult)
		}
	}
	if m.navBlockIndex == -1 {
		var content strings.Builder
		for _, toolResult := range toolResults {
			content.WriteString(toolResultContent(toolResult))
		}
		return content.String(), ""
	}
	if m.navBlockIndex >= 0 && m.navBlockIndex < len(toolResults) {
		return toolResultContent(toolResults[m.navBlockIndex]), ""
	}
	return "", ""
}

func toolResultContent(toolResult *aipb.ToolResult) string {
	if toolResult.GetError() != nil {
		return fmt.Sprintf("Error: %s", toolResult.GetError().GetMessage())
	}
	if structured := toolResult.GetStructuredContent(); structured != nil {
		bytes, _ := pbutil.JSONMarshalPretty(structured)
		return string(bytes)
	}
	return toolResult.GetContent()
}

func (m *Messages) collectMarkdownBlocks(message *aipb.Message) []markdown.Block {
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

func (m *Messages) fullConversationText() string {
	var b strings.Builder
	for _, chatMessage := range m.data.ChatMessages {
		message := chatMessage.GetMessage()
		if message == nil {
			continue
		}
		switch message.Role {
		case aipb.Role_ROLE_USER:
			b.WriteString("## User\n\n")
			for _, block := range message.GetBlocks() {
				if text := block.GetText(); text != "" {
					b.WriteString(text)
					b.WriteString("\n\n")
				}
			}
		case aipb.Role_ROLE_ASSISTANT:
			b.WriteString("## Assistant\n\n")
			for _, block := range ai.FilterBlocks(message.GetBlocks(), ai.BlockTypeThought) {
				b.WriteString("*Thinking:* ")
				b.WriteString(block.GetThought())
				b.WriteString("\n\n")
			}
			for _, block := range ai.FilterBlocks(message.GetBlocks(), ai.BlockTypeText) {
				b.WriteString(block.GetText())
				b.WriteString("\n\n")
			}
			for _, block := range ai.FilterBlocks(message.GetBlocks(), ai.BlockTypeToolCall) {
				toolCall := block.GetToolCall()
				bytes, _ := pbutil.JSONMarshalPretty(toolCall.Arguments)
				b.WriteString(fmt.Sprintf("Tool Call: %s\n```json\n%s\n```\n\n", toolCall.Name, string(bytes)))
			}
		case aipb.Role_ROLE_TOOL:
			b.WriteString("## Tool Result\n\n")
			for _, block := range message.GetBlocks() {
				if toolResult := block.GetToolResult(); toolResult != nil {
					b.WriteString(toolResultContent(toolResult))
					b.WriteString("\n\n")
				}
			}
		case aipb.Role_ROLE_SYSTEM:
			b.WriteString("## System\n\n")
			for _, block := range message.GetBlocks() {
				if text := block.GetText(); text != "" {
					b.WriteString(text)
					b.WriteString("\n\n")
				}
			}
		}
	}
	return b.String()
}

func truncateLines(s string, maxLines int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= maxLines {
		return s
	}
	truncated := strings.Join(lines[:maxLines], "\n")
	return truncated + fmt.Sprintf("\n... (%d more lines)", len(lines)-maxLines)
}

func openInEditor(content, ext string) tea.Cmd {
	input := NewInput()
	return input.openEditor(content, ext)
}
