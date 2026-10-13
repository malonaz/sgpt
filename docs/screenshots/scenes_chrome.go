package main

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	aipb "github.com/malonaz/core/genproto/ai/v1"

	"github.com/malonaz/sgpt/internal/session"
)

const chromeAnswer = "The status bar replaces the title bar: turn state and hints on the left, " +
	"session facts on the right, dropped by priority as the terminal narrows. " +
	"Toasts take the place of the hints, so nothing above ever shifts. " +
	"The tab bar is the only chrome at the top, and the input's border is the focus cue. "

func init() {
	register(
		scene{
			// A settled chat waiting for the next message.
			name:      "chrome-idle",
			title:     "Status bar layout",
			responses: [][]*aipb.Block{{text(chromeAnswer)}},
			drive: func(r *runtime, s *session.Session) {
				r.typeText("where did the title bar go?")
				r.key('j', tea.ModCtrl)
				waitFor(func() bool { return !s.TurnInFlight() && len(s.Messages()) >= 3 }, 5*time.Second)
				r.settle(300 * time.Millisecond)
			},
		},
		scene{
			// Captured mid-turn: spinner, the running tool, elapsed time.
			name:  "chrome-running",
			title: "Slow tests",
			responses: [][]*aipb.Block{
				{toolCall("call_1", "exec_shell", map[string]any{"tool_call_title": "Run the slow test suite", "command": "sleep 3 && echo ok"})},
				{text("Done.\n")},
			},
			drive: func(r *runtime, s *session.Session) {
				r.typeText("run the slow tests")
				r.key('j', tea.ModCtrl)
				waitFor(func() bool { return len(s.PendingToolCallIDs()) > 0 }, 5*time.Second)
				r.settle(200 * time.Millisecond)
				r.key('y', tea.ModAlt)
				r.settle(1300 * time.Millisecond)
			},
		},
		scene{
			// A review pending while a typed message queues: the toast says so.
			name:  "chrome-review-toast",
			title: "Rename pendingReview",
			responses: [][]*aipb.Block{{
				text("Renaming the type and the map that holds it.\n"),
				toolCall("call_1", "diff", map[string]any{"tool_call_title": "Rename pendingReview to awaitingVerdict", "path": "internal/session/review.go", "diff": renameDiff}),
			}},
			drive: func(r *runtime, s *session.Session) {
				r.typeText("rename pendingReview to awaitingVerdict")
				r.key('j', tea.ModCtrl)
				waitFor(func() bool { return len(s.PendingToolCallIDs()) > 0 }, 5*time.Second)
				r.settle(200 * time.Millisecond)
				r.key('\t', 0)
				r.typeText("also rename the accessors")
				r.key('j', tea.ModCtrl)
				r.settle(300 * time.Millisecond)
			},
		},
		scene{
			// A long draft: the input grows with soft-wrapped lines.
			name:  "chrome-input-wrap",
			title: "Draft",
			drive: func(r *runtime, s *session.Session) {
				r.typeText(strings.Repeat("the input grows with every soft-wrapped line of a long draft, ", 4))
				r.settle(200 * time.Millisecond)
			},
		},
		scene{
			// More tabs than fit: titles shrink, then the bar scrolls.
			name:      "chrome-many-tabs",
			title:     "Refactor the session review state machine",
			responses: [][]*aipb.Block{{text(chromeAnswer)}},
			drive: func(r *runtime, s *session.Session) {
				r.typeText("hello")
				r.key('j', tea.ModCtrl)
				waitFor(func() bool { return !s.TurnInFlight() && len(s.Messages()) >= 3 }, 5*time.Second)
				for range 12 {
					r.key('t', tea.ModCtrl)
					r.settle(50 * time.Millisecond)
				}
				r.key('j', tea.ModAlt)
				r.key('j', tea.ModAlt)
				r.key('j', tea.ModAlt)
				r.settle(200 * time.Millisecond)
			},
		},
		scene{
			// An idle ctrl+c: one press opens the quit confirm.
			name:  "chrome-ctrl-c",
			title: "Quit doctrine",
			drive: func(r *runtime, s *session.Session) {
				r.key('c', tea.ModCtrl)
				r.settle(200 * time.Millisecond)
			},
		},
	)
}

func init() {
	register(scene{
		// Paged up: the status bar counts what is below.
		name:  "chrome-scrolled",
		title: "Rename pendingReview",
		responses: [][]*aipb.Block{{
			text("Renaming the type and the map that holds it.\n"),
			toolCall("call_1", "diff", map[string]any{"tool_call_title": "Rename pendingReview to awaitingVerdict", "path": "internal/session/review.go", "diff": renameDiff}),
		}},
		drive: func(r *runtime, s *session.Session) {
			r.typeText("rename pendingReview to awaitingVerdict")
			r.key('j', tea.ModCtrl)
			waitFor(func() bool { return len(s.PendingToolCallIDs()) > 0 }, 5*time.Second)
			r.settle(200 * time.Millisecond)
			r.key(tea.KeyPgUp, 0)
			r.settle(200 * time.Millisecond)
		},
	}, scene{
		// The menu tab: the status bar carries only the global hints.
		name:  "chrome-menu",
		title: "Menu",
		drive: func(r *runtime, s *session.Session) {
			r.key('m', tea.ModAlt)
			r.settle(300 * time.Millisecond)
		},
	})
}
