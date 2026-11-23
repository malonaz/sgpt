package store

import (
	"testing"

	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestSanitizeReplacesNUL(t *testing.T) {
	structured, err := structpb.NewValue(map[string]any{"output": "ELF\x00\x01", "lines": []any{"a\x00b"}})
	if err != nil {
		t.Fatal(err)
	}
	message := ai.NewToolMessage(
		ai.NewToolResultBlock(&aipb.ToolResult{ToolCallId: "1", Result: &aipb.ToolResult_Content{Content: "x\x00y"}}),
		ai.NewToolResultBlock(&aipb.ToolResult{ToolCallId: "2", Result: &aipb.ToolResult_StructuredContent{StructuredContent: structured}}),
	)

	sanitize(message)

	results := message.GetBlocks()
	if got := results[0].GetToolResult().GetContent(); got != "x\uFFFDy" {
		t.Fatalf("content = %q", got)
	}
	fields := results[1].GetToolResult().GetStructuredContent().GetStructValue().GetFields()
	if got := fields["output"].GetStringValue(); got != "ELF\uFFFD\x01" {
		t.Fatalf("output = %q", got)
	}
	if got := fields["lines"].GetListValue().GetValues()[0].GetStringValue(); got != "a\uFFFDb" {
		t.Fatalf("lines[0] = %q", got)
	}
}
