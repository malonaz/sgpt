package tool

import (
	"context"

	aipb "github.com/malonaz/core/genproto/ai/v1"

	"github.com/malonaz/sgpt/internal/permission"
	"github.com/malonaz/sgpt/internal/review"
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
	// Reviews is the chat tree's review queue; sub-agents share it so their
	// calls surface in the root chat.
	Reviews *review.Queue
	// Agent is the session itself: the parent of the sub-agents it launches.
	Agent Agent
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
