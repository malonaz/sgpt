package main

import (
	"time"

	tea "charm.land/bubbletea/v2"
	aipb "github.com/malonaz/core/genproto/ai/v1"

	"github.com/malonaz/sgpt/internal/session"
)

// awaitTurn accepts every call that comes up for review until the turn ends.
func awaitTurn(r *runtime, s *session.Session, timeout time.Duration) {
	waitFor(s.Busy, time.Second)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) && (s.Busy() || len(s.PendingToolCallIDs()) > 0) {
		if len(s.PendingToolCallIDs()) > 0 {
			r.settle(100 * time.Millisecond)
			r.key('y', tea.ModAlt)
		}
		r.settle(50 * time.Millisecond)
	}
	r.settle(300 * time.Millisecond)
}

func init() {
	register(
		scene{
			// One row per call, each with its outcome: reads with a missing
			// file, a passing and a failing command, edits of each kind.
			name:  "tools-rows",
			title: "Tool call rows",
			responses: [][]*aipb.Block{
				{
					toolCall("call_1", "read_files", map[string]any{
						"paths": []any{"internal/session/review.go", "internal/session/verdict.go"},
					}),
					toolCall("call_2", "exec_shell", map[string]any{
						"command": "grep -n pendingReviews internal/session/review.go",
					}),
					toolCall("call_3", "exec_shell", map[string]any{
						"command": "test -f internal/session/verdict.go",
					}),
				},
				{
					toolCall("call_4", "diff", map[string]any{
						"path": "internal/session/review.go",
						"diff": renameDiff,
					}),
					toolCall("call_5", "diff", map[string]any{
						"path": "internal/session/verdict.go",
						"diff": "@@\n+package session\n+\n+// verdict is the user's answer to a reviewed tool call.\n+type verdict struct {\n+\taccepted bool\n+\treason   string\n+}\n",
					}),
				},
				{
					text("Renamed the type and its map, and moved `verdict` into its own file.\n"),
				},
			},
			drive: func(r *runtime, s *session.Session) {
				r.typeText("rename pendingReview and split out verdict")
				r.key('j', tea.ModCtrl)
				awaitTurn(r, s, 10*time.Second)
			},
		},
		scene{
			// A sub-agent launch under review: the tasks as the request body.
			name:  "tools-agents-review",
			title: "Launch agents",
			responses: [][]*aipb.Block{
				{
					toolCall("call_1", "create_agents", map[string]any{
						"context": "Redesign the TUI. Follow `.sgpt/lores/tui/design.md`.",
						"tasks": []any{
							map[string]any{"title": "Timeline rows", "query": "Render each tool call as one row."},
							map[string]any{"title": "Tool summaries", "query": "Make every tool describe itself in one line."},
						},
					}),
				},
				{
					text("Both agents are running; I'll report back when they finish.\n"),
				},
			},
			drive: func(r *runtime, s *session.Session) {
				r.typeText("split the redesign across two agents")
				r.key('j', tea.ModCtrl)
				waitFor(func() bool { return len(s.PendingToolCallIDs()) > 0 || !s.Busy() }, 5*time.Second)
				r.settle(300 * time.Millisecond)
			},
		},
	)
}
