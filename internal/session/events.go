package session

import (
	"sync"
	"time"
)

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

// throttle coalesces bursts of calls into at most one emit per interval,
// always delivering the trailing edge so the final state of a burst renders.
// The zero value is a pass-through.
type throttle struct {
	mu       sync.Mutex
	interval time.Duration
	last     time.Time
	armed    bool
}

func (t *throttle) call(emit func()) {
	t.mu.Lock()
	if t.armed {
		// A trailing emit is already scheduled; it will carry this update.
		t.mu.Unlock()
		return
	}
	if wait := t.interval - time.Since(t.last); wait > 0 {
		t.armed = true
		t.mu.Unlock()
		time.AfterFunc(wait, func() {
			t.mu.Lock()
			t.last = time.Now()
			t.armed = false
			t.mu.Unlock()
			emit()
		})
		return
	}
	t.last = time.Now()
	t.mu.Unlock()
	emit()
}
