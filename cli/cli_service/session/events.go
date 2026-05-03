package session

// Event is emitted by Session to notify the TUI of state changes.
type Event interface{ sessionEvent() }

// RefreshEvent signals the TUI should re-render from current session state.
type RefreshEvent struct{}

func (RefreshEvent) sessionEvent() {}

// ErrorEvent signals a non-fatal error that should be shown as an alert.
type ErrorEvent struct {
	Err error
}

func (ErrorEvent) sessionEvent() {}
