package chat

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"
	"github.com/malonaz/core/go/pbutil"

	chatpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/tools"
)

// toolResultMsg contains the result of a tool execution.
type toolResultMsg struct {
	ToolCallID string
	ToolResult *aipb.ToolResult
}

// toolCancelledMsg indicates the user declined the tool call.
type toolCancelledMsg struct{}

func (m *Model) promptToolCalls(toolCalls []*aipb.ToolCall) {
	if len(toolCalls) == 0 {
		return
	}

	toolCall := toolCalls[0]
	bytes, err := pbutil.JSONMarshalPretty(toolCall.Arguments)
	if err != nil {
		return
	}

	switch toolCall.Name {
	case tools.ShellCommand.Name:
		args, err := tools.ParseShellCommandArgs(bytes)
		if err != nil {
			return
		}
		m.pendingToolCall = toolCall
		m.pendingToolArgs = args
		m.awaitingConfirm = true

	case tools.ReadFiles.Name:
		args, err := tools.ParseReadFilesArgs(bytes)
		if err != nil {
			return
		}
		m.pendingToolCall = toolCall
		m.awaitingConfirm = false
		m.send(toolResultMsg{
			ToolCallID: toolCall.Id,
			ToolResult: m.executeReadFiles(args),
		})
	}
}

func (m *Model) executeReadFiles(args *tools.ReadFilesArgs) *aipb.ToolResult {
	result, err := tools.ExecuteReadFiles(args)
	if err != nil {
		return ai.NewErrorToolResult(tools.ReadFiles.Name, m.pendingToolCall.Id, err)
	}
	return ai.NewToolResult(tools.ReadFiles.Name, m.pendingToolCall.Id, result)
}

func (m *Model) executeShellCommand() tea.Cmd {
	m.awaitingConfirm = false
	toolCall := m.pendingToolCall
	args := m.pendingToolArgs
	if args == nil {
		return nil
	}

	return func() tea.Msg {
		result, err := tools.ExecuteShellCommand(args)
		var toolResult *aipb.ToolResult
		if err != nil {
			toolResult = ai.NewErrorToolResult(tools.ShellCommand.Name, toolCall.Id, err)
		} else {
			toolResult = ai.NewToolResult(tools.ShellCommand.Name, toolCall.Id, result)
		}
		return toolResultMsg{
			ToolCallID: toolCall.Id,
			ToolResult: toolResult,
		}
	}
}

func (m *Model) handleToolResult(msg toolResultMsg) tea.Cmd {
	content, _ := ai.ParseToolResult(msg.ToolResult)
	_ = content

	toolMessage := ai.NewToolMessage(ai.NewToolResultBlock(msg.ToolResult))
	m.chat.Metadata.Messages = append(m.chat.Metadata.Messages, &chatpb.Message{Message: toolMessage})

	m.pendingToolCall = nil
	m.pendingToolArgs = nil

	m.viewport.SetContent(m.renderMessages())
	m.viewport.GotoBottom()

	if msg.ToolResult.GetError() != nil {
		return nil
	}

	m.streaming = true
	return m.startStreaming()
}

func (m *Model) cancelToolCall() {
	m.awaitingConfirm = false
	m.pendingToolCall = nil
	m.pendingToolArgs = nil

	cancelMessage := ai.NewToolMessage(
		ai.NewToolResultBlock(
			ai.NewErrorToolResult("", "", fmt.Errorf("cancelled by user")),
		),
	)
	m.chat.Metadata.Messages = append(m.chat.Metadata.Messages, &chatpb.Message{Message: cancelMessage})

	m.viewport.SetContent(m.renderMessages())
	m.viewport.GotoBottom()
}
