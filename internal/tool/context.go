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
