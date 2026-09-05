package main

import (
	"time"

	tea "charm.land/bubbletea/v2"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/malonaz/sgpt/internal/session"
)

func text(s string) *aipb.Block { return ai.NewTextBlock(s) }

func toolCall(id, name string, arguments map[string]any) *aipb.Block {
	args, err := structpb.NewStruct(arguments)
	if err != nil {
		panic(err)
	}
	return ai.NewToolCallBlock(&aipb.ToolCall{Id: id, Name: name, Arguments: args})
}

const renameDiff = `@@
-type pendingReview struct {
+type awaitingVerdict struct {
@@
-	review, ok := s.pendingReviews[toolCallID]
+	review, ok := s.awaitingVerdicts[toolCallID]
`

func scenes() []scene {
	return []scene{
		{
			// A diff tool call under review: the hero shot.
			name:  "diff-review",
			title: "Rename pendingReview",
			responses: [][]*aipb.Block{
				{
					text("Renaming the type and the map that holds it; the accessor methods keep their names.\n"),
					toolCall("call_1", "diff", map[string]any{
						"path": "internal/session/review.go",
						"diff": renameDiff,
					}),
				},
			},
			drive: func(r *runtime, s *session.Session) {
				r.typeText("rename pendingReview to awaitingVerdict")
				r.key('j', tea.ModCtrl)
				waitFor(func() bool { return len(s.PendingToolCallIDs()) > 0 }, 5*time.Second)
				r.settle(300 * time.Millisecond)
			},
		},
		{
			// Read-only tool auto-executes, then a rich markdown answer.
			name:  "read-and-answer",
			title: "Tool call verdicts",
			responses: [][]*aipb.Block{
				{
					toolCall("call_1", "read_files", map[string]any{
						"paths": []any{"internal/session/review.go"},
					}),
				},
				{
					text("`awaitVerdict` parks the **turn goroutine** on a per-call channel until the UI answers.\n\n" +
						"| Step | Where |\n|---|---|\n| Register the pending review | `internal/session/review.go:8` |\n| Block on the verdict channel | `internal/session/review.go:19` |\n| Cancel with the turn context | `internal/session/review.go:21` |\n\n" +
						"Because the channel lives in `pendingReviews`, the map *is* the review state — nothing else tracks it.\n"),
				},
			},
			drive: func(r *runtime, s *session.Session) {
				r.typeText("how does a tool call wait for my verdict?")
				r.key('j', tea.ModCtrl)
				waitFor(func() bool { return !s.Busy() && len(s.Messages()) >= 4 }, 5*time.Second)
				r.settle(300 * time.Millisecond)
			},
		},
		{
			// Shell command reviewed, accepted, executed, summarised.
			name:  "shell",
			title: "Vet and tests",
			responses: [][]*aipb.Block{
				{
					toolCall("call_1", "exec_shell", map[string]any{
						"command": "go vet ./... && go test ./internal/session/ -run Review -count=1",
					}),
				},
				{
					text("Clean: vet passes and the review tests are green.\n"),
				},
			},
			drive: func(r *runtime, s *session.Session) {
				r.typeText("run vet and the review tests")
				r.key('j', tea.ModCtrl)
				waitFor(func() bool { return len(s.PendingToolCallIDs()) > 0 }, 5*time.Second)
				r.settle(200 * time.Millisecond)
				r.key('y', tea.ModAlt)
				waitFor(func() bool { return !s.Busy() && len(s.Messages()) >= 4 }, 10*time.Second)
				r.settle(300 * time.Millisecond)
			},
		},
		{
			// The keymap overlay.
			name:  "help",
			title: "Keymap",
			drive: func(r *runtime, s *session.Session) {
				r.key('h', tea.ModAlt)
				r.settle(200 * time.Millisecond)
			},
		},
	}
}

func waitFor(condition func() bool, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}
