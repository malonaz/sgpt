package session

// Event is emitted by Session to notify the TUI of state changes.
type Event interface{ sessionEvent() }

// RefreshEvent signals the TUI should re-render from current session state.
type RefreshEvent struct{}

func (RefreshEvent) sessionEvent() {}

// errorPendingEvent notifies the TUI that queued errors are waiting; the
// errors themselves are drained via TakePendingErrors so a dropped
// notification can never lose one.
type errorPendingEvent struct{}

func (errorPendingEvent) sessionEvent() {}

// Errors drains and returns the queued non-fatal errors. Safe to call on any
// event: it returns nil when there is nothing pending.
func (s *Session) Errors() []error {
	return s.takePendingErrors()
}
