package chat

import (
	"context"
	"io"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"
	"google.golang.org/protobuf/proto"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/tools"
)

const renderThrottleInterval = 66 * time.Millisecond

type streamRenderMsg struct{}

type streamDoneMsg struct {
	Err    error
	Blocks []*aipb.Block
}

type chatSavedMsg struct{}

func (m *Model) sendUserMessage() tea.Cmd {
	userInput := strings.TrimSpace(m.textarea.Value())
	if userInput == "" {
		return nil
	}

	m.inputHistory.Add(userInput)
	m.historyNavigating = false
	m.textarea.Reset()

	m.pendingUserMessage = ai.NewUserMessage(ai.NewTextBlock(userInput))
	m.streaming = true
	m.streamError = nil
	m.recalculateLayout()
	m.viewport.GotoBottom()

	return m.startStreaming()
}

func (m *Model) allTools() []*aipb.Tool {
	var toolsList []*aipb.Tool
	if !m.opts.EnableTools {
		return toolsList
	}
	toolsList = append(toolsList, tools.ShellCommand, tools.ReadFiles)
	if m.opts.ToolEngineManager != nil {
		toolsList = append(toolsList, m.opts.ToolEngineManager.GetTools()...)
	}
	return toolsList
}

func (m *Model) startStreaming() tea.Cmd {
	streamCtx, cancel := context.WithCancel(m.ctx)
	m.cancelStream = cancel

	messages := m.messagesForAPI()
	send := m.send

	request := &aiservicepb.TextToTextStreamRequest{
		Model:    m.opts.Model.Name,
		Messages: messages,
		Tools:    m.allTools(),
		Configuration: &aiservicepb.TextToTextConfiguration{
			MaxTokens:       m.opts.MaxTokens,
			Temperature:     m.opts.Temperature,
			ReasoningEffort: m.opts.ReasoningEffort,
		},
	}

	aiClient := m.aiClient
	lastModelUsage := m.lastModelUsage
	totalModelUsage := m.totalModelUsage
	streamingMessage := &m.streamingMessage

	go func() {
		defer func() {
			ai.AggregateModelUsage(totalModelUsage, lastModelUsage)
			*lastModelUsage = aipb.ModelUsage{}
			m.setTitle()
			send(streamRenderMsg{})
		}()

		stream, err := aiClient.TextToTextStream(streamCtx, request)
		if err != nil {
			send(streamDoneMsg{Err: err})
			cancel()
			return
		}

		accumulator := ai.NewTextToTextAccumulator()

		lastRender := time.Now()
		pendingRender := false

		checkRender := func() {
			if time.Since(lastRender) >= renderThrottleInterval {
				send(streamRenderMsg{})
				lastRender = time.Now()
				pendingRender = false
			} else {
				pendingRender = true
			}
		}

		finalize := func(err error) {
			if pendingRender {
				send(streamRenderMsg{})
			}
			send(streamDoneMsg{Err: err, Blocks: accumulator.Message.GetBlocks()})
			cancel()
		}

		for {
			select {
			case <-streamCtx.Done():
				finalize(streamCtx.Err())
				return
			default:
			}

			response, err := stream.Recv()
			if err != nil {
				if err == io.EOF {
					finalize(nil)
				} else {
					finalize(err)
				}
				return
			}
			if err := accumulator.Add(response); err != nil {
				finalize(err)
				return
			}

			*streamingMessage = accumulator.Message

			switch content := response.Content.(type) {
			case *aiservicepb.TextToTextStreamResponse_ModelUsage:
				proto.Merge(lastModelUsage, content.ModelUsage)
			default:
			}

			checkRender()
		}
	}()

	return m.spinner.Tick
}

func (m *Model) finalizeStream(done streamDoneMsg) {
	if m.pendingUserMessage != nil {
		userChatMessage := &sgptpb.Message{Message: m.pendingUserMessage}
		m.chat.Metadata.Messages = append(m.chat.Metadata.Messages, userChatMessage)
		m.pendingUserMessage = nil
	}

	if m.streamingMessage != nil {
		assistantChatMessage := &sgptpb.Message{
			Message: ai.NewAssistantMessage(done.Blocks...),
		}
		if done.Err != nil {
			assistantChatMessage.Error = statusToProto(done.Err)
		}
		m.chat.Metadata.Messages = append(m.chat.Metadata.Messages, assistantChatMessage)
	}

	m.streamingMessage = nil
	m.streaming = false
	m.cancelStream = nil
	m.streamError = done.Err

	wasAtBottom := m.viewport.AtBottom()
	m.recalculateLayout()
	if wasAtBottom {
		m.viewport.GotoBottom()
	}
}
