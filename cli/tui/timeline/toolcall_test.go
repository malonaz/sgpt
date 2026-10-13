package timeline

import (
	"strings"
	"testing"

	aipb "github.com/malonaz/core/genproto/ai/v1"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/malonaz/sgpt/internal/markdown"
)

const longRequest = "a briefing far too long for the timeline"

// foldingRenderer renders every request and folds it.
type foldingRenderer struct{}

func (foldingRenderer) RenderRequest(*aipb.ToolCall) (string, bool) { return longRequest, true }
func (foldingRenderer) FoldsRequest(*aipb.ToolCall) bool            { return true }

func TestFoldedRequestStaysOutOfTheTimeline(t *testing.T) {
	// Multi-line, so the fallback row summary does not quote it either.
	arguments, err := structpb.NewStruct(map[string]any{"context": longRequest + "\n..."})
	if err != nil {
		t.Fatal(err)
	}
	renderer, err := markdown.NewRenderer(80)
	if err != nil {
		t.Fatal(err)
	}
	toolCall := &aipb.ToolCall{Id: "1", Name: "create_agents", Arguments: arguments}
	for name, item := range map[string]*ToolCallItem{
		"streaming": {ToolCall: toolCall, Partial: true, RequestRenderer: foldingRenderer{}},
		"review":    {ToolCall: toolCall, Pending: true, RequestRenderer: foldingRenderer{}},
		"resolved":  {ToolCall: toolCall, Result: &aipb.ToolResult{}, RequestRenderer: foldingRenderer{}},
	} {
		// Expanded is the worst case: a folded request must not show even then.
		rendered := item.Render(RenderContext{Width: 100, Renderer: renderer, Static: true, SelectedSub: -1})
		if strings.Contains(rendered, longRequest) {
			t.Errorf("%s: folded request rendered inline:\n%s", name, rendered)
		}
		if !strings.Contains(rendered, KeyViewRequest.Key.Keys()[0]) {
			t.Errorf("%s: no hint to view the request:\n%s", name, rendered)
		}
		if _, md := item.Request(); md != longRequest {
			t.Errorf("%s: Request() = %q, want the full request", name, md)
		}
	}
}
