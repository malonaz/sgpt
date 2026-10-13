package tool

import (
	"errors"

	aipb "github.com/malonaz/core/genproto/ai/v1"
)

// ErrAgentClosed reports a message sent to a chat whose tab was closed.
var ErrAgentClosed = errors.New("the chat was closed by the user")

// ErrAgentTurnLimit reports a message that was queued but did not start a
// turn: the chat used up its turns on agent messages since the user last spoke.
var ErrAgentTurnLimit = errors.New("the chat reached its limit of turns started by agents; the message is queued until the user resumes it")

// TurnOutcome is how the turn that consumed a delivered message ended: its
// final assistant text, or the error that ended it.
type TurnOutcome struct {
	Text string
	Err  error
}

// Agent is a chat session as the sub-agent system drives it: the launcher of
// sub-agents, or a sub-agent itself.
type Agent interface {
	// Chat returns the session's chat; its name is empty until persisted.
	Chat() *aipb.Chat
	// Deliver hands the session a message from another agent: queued while a
	// turn runs, starting one otherwise. The channel yields the outcome of
	// the turn that consumes the message.
	Deliver(message *aipb.Message) (<-chan TurnOutcome, error)
	// CancelTurn stops the running turn, if any, and its sub-agents' turns.
	CancelTurn()
	// AwaitingReview reports whether one of the session's own tool calls is
	// parked on the user's verdict.
	AwaitingReview() bool
	// Inbox returns a channel closed once a message is queued for the
	// session's next generation — already closed when one is.
	Inbox() <-chan struct{}
	// Price is the chat's spend so far.
	Price() float64
}

// Supervisor is the sub-agent system as a session reports to it.
type Supervisor interface {
	// CancelDescendants stops the running turns of every sub-agent below
	// agent, without the cancellations being reported back to it.
	CancelDescendants(agent Agent)
}
