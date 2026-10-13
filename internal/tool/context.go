package tool

import (
	"context"

	aipb "github.com/malonaz/core/genproto/ai/v1"
)

// historyKey scopes the session-history accessor carried on tool-execution
// contexts.
type historyKey struct{}

// WithHistory stamps a context with an accessor for the executing session's
// message history. The registry is shared across sessions — main chat,
// sub-agents, tabs — so tools that need to know what the model has already
// seen derive it from the history rather than tracking state of their own.
func WithHistory(ctx context.Context, history func() []*aipb.Message) context.Context {
	return context.WithValue(ctx, historyKey{}, history)
}

// History returns the executing session's message history; nil when
// executing outside a session.
func History(ctx context.Context) []*aipb.Message {
	history, ok := ctx.Value(historyKey{}).(func() []*aipb.Message)
	if !ok {
		return nil
	}
	return history()
}

// attachKey scopes the attachment sink carried on tool-execution contexts.
type attachKey struct{}

// WithAttach stamps a context with a sink for content a tool result cannot
// carry (tool messages hold only tool_result blocks): attached blocks reach
// the model as a user message right after the tool message.
func WithAttach(ctx context.Context, attach func(blocks ...*aipb.Block)) context.Context {
	return context.WithValue(ctx, attachKey{}, attach)
}

// Attach hands blocks to the executing turn; it reports false when executing
// outside a turn, where nothing would deliver them.
func Attach(ctx context.Context, blocks ...*aipb.Block) bool {
	attach, ok := ctx.Value(attachKey{}).(func(blocks ...*aipb.Block))
	if !ok {
		return false
	}
	attach(blocks...)
	return true
}
