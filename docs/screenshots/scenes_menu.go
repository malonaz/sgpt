package main

import (
	"time"

	tea "charm.land/bubbletea/v2"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/malonaz/sgpt/internal/session"
	"github.com/malonaz/sgpt/internal/store"
	"github.com/malonaz/sgpt/internal/tool"
)

const chatsParent = "organizations/acme/users/ada/chats/"

// seededChat describes one history entry; age drives both create and update
// time, so the list's order matches what the ages say.
type seededChat struct {
	id       string
	title    string
	age      time.Duration
	price    float64
	favorite bool
	tags     []string
	parentID string
}

var seededChats = []seededChat{
	{id: "a1f04c2e", title: "Rename pendingReview to awaitingVerdict", age: 2 * time.Hour, price: 0.42, favorite: true, tags: []string{"malonaz/sgpt"}},
	{id: "b73e91d0", title: "Audit session review state", age: 110 * time.Minute, price: 0.18, parentID: "a1f04c2e"},
	{id: "c2d8a4f1", title: "Find every caller of awaitVerdict", age: 105 * time.Minute, price: 0.07, parentID: "a1f04c2e"},
	{id: "d91b6e3a", title: "Why does the timeline re-render on every tick?", age: 5 * time.Hour, price: 1.37, tags: []string{"malonaz/sgpt", "perf"}},
	{id: "e40c7b2d", title: "Postgres AIP filter transpiler: wildcard semantics", age: 26 * time.Hour, price: 0.86, favorite: true, tags: []string{"malonaz/core"}},
	{id: "f18a3d9c", title: "Draft the release notes for v0.9", age: 30 * time.Hour, price: 0.12},
	{id: "0a6e2f47", title: "Flaky TestStreamCancel on CI", age: 3 * 24 * time.Hour, price: 2.04, tags: []string{"ci"}},
	{id: "1b9d4c80", title: "Bisect the regression", age: 3*24*time.Hour - time.Hour, price: 0.31, parentID: "0a6e2f47"},
	{id: "2c7f5a13", title: "Terraform plan for the new ECS service", age: 6 * 24 * time.Hour, price: 0.55, tags: []string{"infra"}},
	{id: "3d0e8b64", title: "Explain bubbletea v2 key handling", age: 9 * 24 * time.Hour, price: 0.04},
	{id: "4e2a9c75", title: "Glamour table rendering width bug", age: 14 * 24 * time.Hour, price: 0.009},
	{id: "5f3b0d86", title: "Write the design doc for sub-agent messaging", age: 40 * 24 * time.Hour, price: 3.12, tags: []string{"design"}},
	{id: "6a4c1e97", title: "Migrate lipgloss v1 to v2", age: 70 * 24 * time.Hour, price: 1.08},
	{id: "7b5d2fa8", title: "Benchmark the markdown renderer", age: 120 * 24 * time.Hour, price: 0.22},
}

func seedChats() []*aipb.Chat {
	chats := make([]*aipb.Chat, 0, len(seededChats))
	for _, seed := range seededChats {
		at := timestamppb.New(time.Now().Add(-seed.age))
		chat := &aipb.Chat{Name: chatsParent + seed.id, Title: seed.title, CreateTime: at, UpdateTime: at, Price: seed.price}
		store.SetCategory(chat, "chat")
		if seed.parentID != "" {
			store.SetCategory(chat, "agent")
			store.SetParentChatID(chat, chatsParent+seed.parentID)
		}
		store.SetFavoriteLabel(chat, seed.favorite)
		store.SetTags(chat, seed.tags)
		store.SetCurrentModel(chat, model)
		chats = append(chats, chat)
	}
	return chats
}

// persistedToolCall is a call as the server stores it: annotated with the
// handler that resolves it to a registered tool.
func persistedToolCall(id, name, handlerID string, arguments map[string]any) *aipb.Block {
	block := toolCall(id, name, arguments)
	block.GetToolCall().Annotations = map[string]string{tool.ToolHandlerIDAnnotation: handlerID}
	return block
}

// readFilesResult is read_files' structured result for one file.
func readFilesResult(path, content string) *structpb.Value {
	value, err := structpb.NewValue(map[string]any{"files": []any{map[string]any{"path": path, "content": content}}})
	if err != nil {
		panic(err)
	}
	return value
}

// seedMessages gives the newest chat a short conversation to preview.
func seedMessages() map[string][]*aipb.Message {
	return map[string][]*aipb.Message{
		chatsParent + "a1f04c2e": {
			ai.NewUserMessage(text("rename pendingReview to awaitingVerdict")),
			ai.NewAssistantMessage(text("Reading the review state first.\n"), persistedToolCall("call_1", "read_files", tool.HandlerIDReadFiles, map[string]any{"paths": []any{"internal/session/review.go"}})),
			ai.NewToolMessage(ai.NewToolResultBlock(ai.NewStructuredToolResult("read_files", "call_1", readFilesResult(
				"internal/session/review.go", "package session\n\ntype pendingReview struct {\n\tverdict chan verdict\n}\n")))),
			ai.NewAssistantMessage(text("Renamed the type and the map that holds it; the accessor methods keep their names.\n\n" +
				"| File | Change |\n|---|---|\n| `internal/session/review.go` | type and map renamed |\n| `internal/session/session.go` | field renamed |\n")),
		},
	}
}

func menuScene(name string, drive func(r *runtime, s *session.Session)) scene {
	return scene{name: name, title: "Chats", chats: seedChats(), messages: seedMessages(), drive: func(r *runtime, s *session.Session) {
		r.key('m', tea.ModAlt)
		r.settle(300 * time.Millisecond)
		drive(r, s)
	}}
}

func init() {
	register(
		// The list with favorites, sub-agent chats and tags, plus a loaded
		// preview.
		menuScene("menu", func(r *runtime, _ *session.Session) {
			r.key('s', tea.ModAlt)
			r.settle(300 * time.Millisecond)
			r.key('n', tea.ModCtrl)
			r.settle(300 * time.Millisecond)
		}),
		// A server-side search narrowing the history.
		menuScene("menu-filter", func(r *runtime, _ *session.Session) {
			r.typeText("review")
			r.settle(600 * time.Millisecond)
		}),
		// The delete confirm (rendered by the app's ConfirmMsg handling).
		menuScene("menu-delete", func(r *runtime, _ *session.Session) {
			r.key('n', tea.ModCtrl)
			r.settle(200 * time.Millisecond)
			r.key('d', tea.ModAlt)
			r.settle(300 * time.Millisecond)
		}),
		// The help modal over the Chats tab.
		menuScene("menu-help", func(r *runtime, _ *session.Session) {
			r.key('h', tea.ModAlt)
			r.settle(200 * time.Millisecond)
		}),
		scene{
			// The info modal after a turn with a tool call.
			name:  "info",
			title: "Tool call verdicts",
			responses: [][]*aipb.Block{
				{toolCall("call_1", "read_files", map[string]any{"paths": []any{"internal/session/review.go"}})},
				{text("The review state lives in `pendingReviews`.\n")},
			},
			drive: func(r *runtime, s *session.Session) {
				r.typeText("where does review state live?")
				r.key('j', tea.ModCtrl)
				waitFor(func() bool { return !s.Busy() && len(s.Messages()) >= 4 }, 5*time.Second)
				r.settle(200 * time.Millisecond)
				r.key('i', tea.ModAlt)
				r.settle(300 * time.Millisecond)
			},
		},
		scene{
			// The tool picker with a filter typed.
			name:  "picker",
			title: "Tools",
			drive: func(r *runtime, _ *session.Session) {
				r.key('t', tea.ModAlt|tea.ModShift)
				r.settle(200 * time.Millisecond)
				r.typeText("re")
				r.settle(200 * time.Millisecond)
			},
		},
	)
}
