package session

import (
	"context"
	"fmt"

	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"

	"github.com/malonaz/sgpt/internal/debug"
)

// turn owns one full exchange with the model: the generation loop, tool
// resolution and the requeue-on-failure contract. It exists exactly while the
// exchange runs; everything that outlives it (the message mirror, the input
// queues, usage) stays on Session.
type turn struct {
	session *Session
	// ctx scopes every blocking operation of the turn — streams, tool
	// executions, review waits — so one cancel aborts it wherever it is.
	ctx    context.Context
	cancel context.CancelFunc
}

func newTurn(s *Session) *turn {
	ctx, cancel := context.WithCancel(s.ctx)
	return &turn{session: s, ctx: ctx, cancel: cancel}
}

// run executes the turn to completion: generate → resolve tool calls → loop.
// Every loop iteration flushes chat state before streaming, and the turn
// flushes once more on exit, so a turn never leaves unpersisted chat state
// behind.
func (t *turn) run() {
	s := t.session
	defer s.flushChat()
	for {
		s.flushChat()
		inputMessages := s.takeInputMessages()
		generatedMessage, err := t.stream(inputMessages)

		s.mu.Lock()
		ai.AggregateModelUsage(s.totalModelUsage, s.lastModelUsage)
		*s.lastModelUsage = aipb.ModelUsage{}
		s.mu.Unlock()

		if err != nil {
			// The failed inputs are re-queued (the server excluded them from
			// history), so the next turn resumes exactly where this one died.
			s.requeueInputMessages(inputMessages)
			s.refresh()
			s.notifyTurnComplete("", err)
			return
		}

		var toolCalls []*aipb.ToolCall
		for _, block := range ai.FilterBlocks(generatedMessage.GetBlocks(), ai.BlockTypeToolCall) {
			toolCalls = append(toolCalls, block.GetToolCall())
		}

		if len(toolCalls) == 0 {
			// An interjection arrived mid-generation: answer it before the
			// turn ends.
			if s.hasQueuedMessages() {
				continue
			}
			s.maybeGenerateTitle()
			s.refresh()
			s.notifyTurnComplete(s.lastAssistantText(), nil)
			return
		}

		t.executeToolCalls(generatedMessage, toolCalls)

		// Cancelled mid-review or mid-execution: the remaining calls were
		// resolved as cancelled and their results queued for the next turn —
		// end this one cleanly instead of streaming on a dead context.
		if t.ctx.Err() != nil {
			s.refresh()
			s.notifyTurnComplete("", t.ctx.Err())
			return
		}
	}
}

// executeToolCalls resolves every tool call of the assistant message strictly
// sequentially, then queues the results as input for the next generation.
//
// Review is a blocking await inside this loop: a call needing approval parks
// the turn goroutine until the user answers (see awaitVerdict). There is no
// review state to track — a call is "pending" precisely while the loop is
// waiting on it, and a verdict produces a terminal result immediately.
func (t *turn) executeToolCalls(assistantMessage *aipb.Message, toolCalls []*aipb.ToolCall) {
	s := t.session
	resultBlocks := make([]*aipb.Block, 0, len(toolCalls))
	for _, toolCall := range toolCalls {
		debug.LogProto(toolCall.GetName(), toolCall)
		s.resolveToolCall(t.ctx, toolCall, false)
		s.refresh()
		resultBlocks = append(resultBlocks, ai.NewToolResultBlock(toolCall.GetResult()))
	}

	// Persist the results living inside the assistant message's tool call
	// blocks. The session context — not the turn's — so a cancelled turn
	// still records its results.
	if assistantMessage.GetName() != "" {
		if _, err := s.store.UpdateMessage(s.ctx, assistantMessage, "blocks"); err != nil {
			s.emitError(fmt.Errorf("persisting tool call results: %w", err))
		}
	}

	toolMessage := ai.NewToolMessage(resultBlocks...)
	s.mu.Lock()
	s.messages = append(s.messages, toolMessage)
	// The tool message is persisted server-side as input to the next turn.
	s.pendingInputMessages = append(s.pendingInputMessages, toolMessage)
	s.invalidatePrice()
	s.mu.Unlock()
	s.refresh()
}
