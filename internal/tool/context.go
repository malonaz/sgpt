package tool

import (
	"context"

	aipb "github.com/malonaz/core/genproto/ai/v1"

	"github.com/malonaz/sgpt/internal/permission"
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

// Caller is the session on whose behalf a tool call runs — what a tool that
// spawns further work (the agent tool) may hand down, and no more.
type Caller struct {
	// Chat is the session's chat resource name; empty until it is persisted.
	Chat string
	// Depth is 0 for a chat the user opened, one more per sub-agent level.
	Depth int
	// Model is the session's current model.
	Model *aipb.Model
	// Tools are the user-facing tool names enabled in the session.
	Tools []string
	// Policy governs the session's tool calls; children derive from it.
	Policy *permission.Policy
}

type callerKey struct{}

// WithCaller stamps a context with an accessor for the executing session's
// identity. An accessor rather than a value: the chat is created lazily and
// the tool selection changes mid-session.
func WithCaller(ctx context.Context, caller func() Caller) context.Context {
	return context.WithValue(ctx, callerKey{}, caller)
}

// CallerFromContext returns the executing session's identity; false when
// executing outside a session.
func CallerFromContext(ctx context.Context) (Caller, bool) {
	caller, ok := ctx.Value(callerKey{}).(func() Caller)
	if !ok {
		return Caller{}, false
	}
	return caller(), true
}
