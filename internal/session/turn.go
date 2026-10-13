package session

import (
	"context"
	"fmt"

	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"

	"github.com/malonaz/sgpt/internal/debug"
	"github.com/malonaz/sgpt/internal/store"
	"github.com/malonaz/sgpt/internal/tool"
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
	// inputs are the user messages the turn was started for.
	inputs []*aipb.Message
	// waiters learn how the turn ends: they were waiting on a message it
	// consumed. Guarded by the session lock.
	waiters []func(tool.TurnOutcome)
	// attachments are tool.Attach'd blocks awaiting the next tool message;
	// unlocked since tools only run on the turn goroutine.
	attachments []*aipb.Block
}

func newTurn(s *Session) *turn {
	t := &turn{session: s}
	t.ctx, t.cancel = context.WithCancel(tool.WithAttach(s.ctx, t.attach))
	return t
}

// complete tells the turn's waiters how it ended. Called once the turn's
// outcome is known, before it is released.
func (t *turn) complete(text string, err error) {
	s := t.session
	s.mu.Lock()
	waiters := t.waiters
	t.waiters = nil
	s.mu.Unlock()
	for _, waiter := range waiters {
		waiter(tool.TurnOutcome{Text: text, Err: err})
	}
}

func (t *turn) attach(blocks ...*aipb.Block) {
	t.attachments = append(t.attachments, blocks...)
}

// run executes the turn to completion: generate → resolve tool calls → loop.
// Chat state is flushed after every generation (before tool review can park
// the turn) and once more on exit, so a turn never leaves unpersisted chat
// state behind and the server's usage/price rollup is adopted promptly.
// Returns the error that ended the turn, nil on a final answer.
func (t *turn) run() error {
	s := t.session
	defer s.flushChat()
	for {
		inputMessages := s.takeInputMessages(t)
		generatedMessage, err := t.stream(inputMessages)

		s.mu.Lock()
		*s.lastModelUsage = aipb.ModelUsage{}
		s.mu.Unlock()
		s.flushChat()

		if err != nil {
			// The failed inputs are re-queued (the server excluded them from
			// history), so the next turn resumes exactly where this one died.
			s.requeueInputMessages(inputMessages)
			s.refresh()
			t.complete("", err)
			return err
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
			t.complete(s.lastAssistantText(), nil)
			return nil
		}

		t.executeToolCalls(generatedMessage, toolCalls)

		// Cancelled mid-review or mid-execution: the remaining calls were
		// resolved as cancelled and their results queued for the next turn —
		// end this one cleanly instead of streaming on a dead context.
		if err := t.ctx.Err(); err != nil {
			s.refresh()
			t.complete("", err)
			return err
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

	inputMessages := []*aipb.Message{ai.NewToolMessage(resultBlocks...)}
	if len(t.attachments) > 0 {
		inputMessages = append(inputMessages, store.NewAttachmentMessage(t.attachments))
		t.attachments = nil
	}
	s.mu.Lock()
	s.messages = append(s.messages, inputMessages...)
	// Persisted server-side as input to the next turn.
	s.pendingInputMessages = append(s.pendingInputMessages, inputMessages...)
	s.mu.Unlock()
	s.refresh()
}
