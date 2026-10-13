package main

import (
	"time"

	tea "charm.land/bubbletea/v2"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"

	"github.com/malonaz/sgpt/internal/session"
	"github.com/malonaz/sgpt/internal/store"
)

func thought(s string) *aipb.Block { return ai.NewThoughtBlock(s) }

// selectUp steps the timeline cursor up count items and toggles the selection
// open. Reviews leave the timeline focused on the last reviewed call; other
// scenes pass focus to move there first.
func selectUp(r *runtime, focus bool, up int) {
	if focus {
		r.key(tea.KeyTab, 0)
		r.settle(100 * time.Millisecond)
	}
	for range up {
		r.key('[', tea.ModAlt)
	}
	r.key(tea.KeyEnter, 0)
	r.settle(200 * time.Millisecond)
}

// agenticTurn is a realistic multi-step turn: investigations, a note, an edit,
// then checks (one fails), and the answer. Calls carry the titles models
// write once tool_call_title is on.
func agenticTurn(edit *aipb.Block) [][]*aipb.Block {
	return [][]*aipb.Block{
		{
			thought("The rename touches the review state. First find what the lores say about it, then read the file."),
			toolCall("call_1", "search_lores", map[string]any{"tool_call_title": "Check the lores for review conventions", "query": "review|verdict"}),
			toolCall("call_2", "read_files", map[string]any{"tool_call_title": "Read the review state", "paths": []any{"internal/session/review.go"}}),
		},
		{
			thought("Two places reference the map. Check nothing else does."),
			toolCall("call_3", "exec_shell", map[string]any{"tool_call_title": "Find every use of pendingReview", "command": "grep -rn pendingReview internal/"}),
			toolCall("call_4", "exec_shell", map[string]any{"tool_call_title": "Size up review.go", "command": "wc -l internal/session/review.go"}),
			toolCall("call_5", "exec_shell", map[string]any{"tool_call_title": "Find callers of awaitVerdict", "command": "grep -rln awaitVerdict internal/"}),
		},
		{
			text("Only `review.go` references the type and its map, so the rename stays local.\n"),
			edit,
		},
		{
			toolCall("call_7", "exec_shell", map[string]any{"tool_call_title": "Build everything", "command": "go build ./..."}),
			toolCall("call_8", "exec_shell", map[string]any{"tool_call_title": "Check formatting", "command": "gofmt -l internal/"}),
		},
		{
			text("Renamed `pendingReview` to `awaitingVerdict`, along with the map that holds it.\n\n" +
				"- The accessor methods keep their names, so callers are untouched.\n" +
				"- `go build` fails here only because the fixture has no module; `gofmt` is clean.\n"),
		},
	}
}

// renameEdit is the agentic turn's edit; scenes sharing the fixtures re-read
// instead, so the edit applies once per run.
func renameEdit() *aipb.Block {
	return toolCall("call_6", "diff", map[string]any{"tool_call_title": "Rename the type and its map", "path": "internal/session/review.go", "diff": renameDiff})
}

func rereadEdit() *aipb.Block {
	return toolCall("call_6", "read_files", map[string]any{"tool_call_title": "Re-read the renamed state", "paths": []any{"internal/session/review.go"}})
}

// followUp is a short second turn, so turn boundaries show.
var followUp = [][]*aipb.Block{
	{
		toolCall("call_9", "exec_shell", map[string]any{"tool_call_title": "List the session package", "command": "ls internal/session"}),
		toolCall("call_10", "search_lores", map[string]any{"tool_call_title": "Look for a testing lore", "query": "testing"}),
	},
	{text("No test references either name, so nothing else needs to change.\n")},
}

func init() {
	register(
		scene{
			// A user turn written in markdown: its band must stay one color.
			name:  "timeline-user-markdown",
			title: "Rename with care",
			responses: [][]*aipb.Block{
				{text("Will do: the accessors keep their names and `go vet` stays clean.\n")},
			},
			drive: func(r *runtime, s *session.Session) {
				for index, line := range []string{
					"rename `pendingReview` to **awaitingVerdict** in `review.go`",
					"",
					"- keep the accessor names",
					"- then run `go vet ./...`",
					"",
					"```go",
					"type awaitingVerdict struct{}",
					"```",
				} {
					if index > 0 {
						r.key(tea.KeyEnter, 0)
					}
					r.typeText(line)
				}
				r.key('j', tea.ModCtrl)
				awaitTurn(r, s, 10*time.Second)
				// Selected, the turn keeps its band: only the gutter bar lights.
				selectUp(r, true, 1)
			},
		},

		scene{
			// Two finished turns: each folds to its prompt, one work row and
			// the answer.
			name:      "timeline-agentic",
			title:     "Rename pendingReview",
			responses: append(agenticTurn(renameEdit()), followUp...),
			drive: func(r *runtime, s *session.Session) {
				r.typeText("rename pendingReview to awaitingVerdict")
				r.key('j', tea.ModCtrl)
				awaitTurn(r, s, 15*time.Second)
				r.key(tea.KeyEscape, 0)
				r.typeText("do any tests need updating?")
				r.key('j', tea.ModCtrl)
				awaitTurn(r, s, 10*time.Second)
			},
		},
		scene{
			// The first turn's work row opened: every step, nested under it.
			name:      "timeline-work-expanded",
			title:     "Rename pendingReview",
			responses: append(agenticTurn(rereadEdit()), followUp...),
			drive: func(r *runtime, s *session.Session) {
				r.typeText("rename pendingReview to awaitingVerdict")
				r.key('j', tea.ModCtrl)
				awaitTurn(r, s, 15*time.Second)
				r.key(tea.KeyEscape, 0)
				r.typeText("do any tests need updating?")
				r.key('j', tea.ModCtrl)
				awaitTurn(r, s, 10*time.Second)
				selectUp(r, false, 3)
			},
		},
		scene{
			// A turn mid-work: its steps show as they happen, and the call
			// waiting on you is a card.
			name:  "timeline-live",
			title: "Rename pendingReview",
			responses: [][]*aipb.Block{
				agenticTurn(nil)[0],
				agenticTurn(nil)[1],
				{
					text("Only `review.go` references the type and its map, so the rename stays local.\n"),
					renameEdit(),
				},
			},
			drive: func(r *runtime, s *session.Session) {
				r.typeText("rename pendingReview to awaitingVerdict")
				r.key('j', tea.ModCtrl)
				waitFor(func() bool { return len(s.PendingToolCallIDs()) > 0 }, 10*time.Second)
				r.settle(300 * time.Millisecond)
			},
		},
		scene{
			// One call opened: request, then the capped result.
			name:  "timeline-expanded-call",
			title: "Find references",
			responses: [][]*aipb.Block{
				{toolCall("call_1", "exec_shell", map[string]any{"tool_call_title": "Show the review state with line numbers", "command": "grep -n . internal/session/review.go"})},
				{text("`review.go` is the only file involved.\n")},
			},
			drive: func(r *runtime, s *session.Session) {
				r.typeText("where is the review state?")
				r.key('j', tea.ModCtrl)
				awaitTurn(r, s, 10*time.Second)
				selectUp(r, false, 0)
			},
		},
		scene{
			// A thought opened: muted reasoning behind a rule.
			name:  "timeline-thought",
			title: "Verdict timeouts",
			responses: [][]*aipb.Block{
				{
					thought("The verdict channel is unbuffered and the turn goroutine blocks on it.\n\n" +
						"If the UI never answers, the select on ctx.Done() is the only way out, so cancelling the turn must cancel that context. " +
						"A timeout would silently reject the call, which is worse than waiting: the user may simply be reading the diff."),
					text("There is no timeout, on purpose: a review waits until you answer or cancel the turn.\n"),
				},
			},
			drive: func(r *runtime, s *session.Session) {
				r.typeText("should reviews time out?")
				r.key('j', tea.ModCtrl)
				awaitTurn(r, s, 10*time.Second)
				selectUp(r, true, 1)
			},
		},
		scene{
			// A message another agent sent: Agent rule, sender, muted note.
			name:  "timeline-agent-message",
			title: "Audit",
			responses: [][]*aipb.Block{
				{text("The audit agrees with the rename: all three call sites live in `review.go`.\n")},
			},
			drive: func(r *runtime, s *session.Session) {
				s.Deliver(store.NewAgentMessage("audit agent",
					"[operations/7f3a: completed]\nFound three call sites of `pendingReviews`, all in `internal/session/review.go`:\n\n"+
						"- line 8, registering the review\n- line 19, blocking on the verdict\n- line 21, cancelling with the turn\n"))
				awaitTurn(r, s, 10*time.Second)
			},
		},
	)
}
