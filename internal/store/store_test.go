package store

import (
	"testing"

	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"
	"google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func toolCallBlock(id, name string) *aipb.Block {
	return ai.NewToolCallBlock(&aipb.ToolCall{Id: id, Name: name})
}

func toolResultBlock(toolCallID string) *aipb.Block {
	return ai.NewToolResultBlock(&aipb.ToolResult{ToolCallId: toolCallID})
}

// toolCallIDs projects the calls to their ids for concise assertions.
func toolCallIDs(toolCalls []*aipb.ToolCall) []string {
	ids := make([]string, 0, len(toolCalls))
	for _, toolCall := range toolCalls {
		ids = append(ids, toolCall.GetId())
	}
	return ids
}

func assertIDs(t *testing.T, got []*aipb.ToolCall, want ...string) {
	t.Helper()
	gotIDs := toolCallIDs(got)
	if len(gotIDs) != len(want) {
		t.Fatalf("orphans = %v, want %v", gotIDs, want)
	}
	for i, id := range want {
		if gotIDs[i] != id {
			t.Fatalf("orphans = %v, want %v", gotIDs, want)
		}
	}
}

func TestOrphanedToolCallsHealthyHistory(t *testing.T) {
	messages := []*aipb.Message{
		ai.NewUserMessage(ai.NewTextBlock("hi")),
		ai.NewAssistantMessage(toolCallBlock("call-1", "shell")),
		ai.NewToolMessage(toolResultBlock("call-1")),
	}
	if orphans := OrphanedToolCalls(messages); len(orphans) != 0 {
		t.Fatalf("orphans = %v, want none", toolCallIDs(orphans))
	}
}

// The failure this whole repair exists for: the assistant message was
// persisted, its tool message never was.
func TestOrphanedToolCallsUnansweredCall(t *testing.T) {
	messages := []*aipb.Message{
		ai.NewUserMessage(ai.NewTextBlock("hi")),
		ai.NewAssistantMessage(toolCallBlock("call-1", "shell")),
	}
	assertIDs(t, OrphanedToolCalls(messages), "call-1")
}

// Order matters: results must be emitted in call order.
func TestOrphanedToolCallsPartiallyAnswered(t *testing.T) {
	messages := []*aipb.Message{
		ai.NewAssistantMessage(
			toolCallBlock("call-1", "shell"),
			toolCallBlock("call-2", "diff"),
			toolCallBlock("call-3", "read"),
		),
		ai.NewToolMessage(toolResultBlock("call-2")),
	}
	assertIDs(t, OrphanedToolCalls(messages), "call-1", "call-3")
}

// A soft-deleted assistant message is already out of the provider history, so
// its calls need no result.
func TestOrphanedToolCallsIgnoresDeleted(t *testing.T) {
	deletedMessage := ai.NewAssistantMessage(toolCallBlock("call-1", "shell"))
	deletedMessage.DeleteTime = timestamppb.Now()
	if orphans := OrphanedToolCalls([]*aipb.Message{deletedMessage}); len(orphans) != 0 {
		t.Fatalf("orphans = %v, want none", toolCallIDs(orphans))
	}
}

// Same for a message the server flagged as failed.
func TestOrphanedToolCallsIgnoresFailed(t *testing.T) {
	failedMessage := ai.NewAssistantMessage(toolCallBlock("call-1", "shell"))
	failedMessage.Status = &status.Status{Code: 13, Message: "boom"}
	if orphans := OrphanedToolCalls([]*aipb.Message{failedMessage}); len(orphans) != 0 {
		t.Fatalf("orphans = %v, want none", toolCallIDs(orphans))
	}
}

// A result attached to the call itself still has to reach the provider as a
// tool message, so the call is reported until one exists.
func TestOrphanedToolCallsAttachedResultStillNeedsToolMessage(t *testing.T) {
	toolCall := &aipb.ToolCall{Id: "call-1", Name: "shell"}
	toolCall.Result = ai.NewToolResult("shell", "call-1", "done")
	messages := []*aipb.Message{ai.NewAssistantMessage(ai.NewToolCallBlock(toolCall))}
	assertIDs(t, OrphanedToolCalls(messages), "call-1")
}

func TestOrphanedToolCallsEmptyHistory(t *testing.T) {
	if orphans := OrphanedToolCalls(nil); len(orphans) != 0 {
		t.Fatalf("orphans = %v, want none", toolCallIDs(orphans))
	}
}
