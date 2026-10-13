package timeline

import (
	"cmp"
	"strings"

	aipb "github.com/malonaz/core/genproto/ai/v1"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/malonaz/sgpt/internal/store"
	"github.com/malonaz/sgpt/internal/tool"
)

// Documents are what the editor and the clipboard receive: plain markdown,
// unabridged, with multi-line values kept as real lines (never JSON-escaped).

// document renders the call: its identity, the request, then the full result.
func (i *ToolCallItem) document() string {
	summary := i.summary()
	facts := []string{"`" + i.ToolCall.GetName() + "`"}
	if summary.Outcome != "" {
		facts = append(facts, summary.Outcome)
	}
	sections := []string{
		"### " + i.heading(summary),
		strings.Join(facts, " · "),
		"**Request**",
		i.requestDocument(),
	}
	if i.Result != nil {
		sections = append(sections, "**Result**", resultDocument(i.Result))
	}
	return strings.Join(sections, "\n\n")
}

// heading names the call: its title, else what it does.
func (i *ToolCallItem) heading(summary tool.Summary) string {
	return cmp.Or(tool.Title(i.ToolCall), strings.TrimSpace(summary.Verb+" "+summary.Subject))
}

// Request is the call's full request, titled, for reading in a modal.
func (i *ToolCallItem) Request() (title, md string) {
	return i.heading(i.summary()), i.requestDocument()
}

// requestDocument is the tool's own request markdown (a diff, a command),
// else the arguments as plain fields.
func (i *ToolCallItem) requestDocument() string {
	if i.RequestRenderer != nil {
		if md, ok := i.RequestRenderer.RenderRequest(i.ToolCall); ok {
			return md
		}
	}
	return tool.Fence("yaml", formatFields(requestFields(i.ToolCall), 0, fieldInk{}))
}

// resultDocument keeps everything: top-level multi-line strings (stdout, file
// contents) get fences of their own, the rest reads as plain fields.
func resultDocument(result *aipb.ToolResult) string {
	if err := result.GetError(); err != nil {
		return tool.Fence("", "Error: "+err.GetMessage())
	}
	structured := result.GetStructuredContent().GetStructValue()
	if structured == nil {
		if content := result.GetContent(); content != "" {
			return tool.Fence("", content)
		}
		return "_(no output)_"
	}
	var sections []string
	scalars := map[string]*structpb.Value{}
	for _, key := range sortedKeys(structured.GetFields()) {
		value := structured.GetFields()[key]
		if text := value.GetStringValue(); strings.Contains(text, "\n") {
			sections = append(sections, "`"+key+"`\n\n"+tool.Fence("", text))
			continue
		}
		scalars[key] = value
	}
	if len(scalars) > 0 {
		sections = append([]string{tool.Fence("yaml", formatFields(scalars, 0, fieldInk{}))}, sections...)
	}
	return strings.Join(sections, "\n\n")
}

// ConversationText renders the entire chat as one markdown document, each
// tool call beside its result — what alt+shift+o opens in $EDITOR.
func ConversationText(messages []*aipb.Message, requestRenderer RequestRenderer) string {
	toolCallIDToResult := map[string]*aipb.ToolResult{}
	for _, message := range messages {
		for _, block := range message.GetBlocks() {
			if result := block.GetToolResult(); result != nil {
				toolCallIDToResult[result.GetToolCallId()] = result
			}
		}
	}
	var sections []string
	for _, message := range messages {
		switch message.GetRole() {
		case aipb.Role_ROLE_USER:
			sections = append(sections, "## "+cmp.Or(store.MessageSender(message), "User"))
			sections = append(sections, messageTexts(message)...)
		case aipb.Role_ROLE_ASSISTANT:
			sections = append(sections, "## Assistant")
			for _, block := range message.GetBlocks() {
				switch {
				case block.GetThought() != "":
					sections = append(sections, "> "+strings.ReplaceAll(block.GetThought(), "\n", "\n> "))
				case block.GetText() != "":
					sections = append(sections, block.GetText())
				case block.GetToolCall() != nil:
					toolCall := block.GetToolCall()
					item := &ToolCallItem{
						ToolCall:        toolCall,
						Result:          cmp.Or(toolCall.GetResult(), toolCallIDToResult[toolCall.GetId()]),
						RequestRenderer: requestRenderer,
					}
					sections = append(sections, item.document())
				}
			}
		case aipb.Role_ROLE_SYSTEM:
			sections = append(sections, "## System")
			sections = append(sections, messageTexts(message)...)
		}
	}
	return strings.Join(sections, "\n\n") + "\n"
}

func messageTexts(message *aipb.Message) []string {
	var texts []string
	for _, block := range message.GetBlocks() {
		if text := block.GetText(); text != "" {
			texts = append(texts, text)
		}
	}
	return texts
}
