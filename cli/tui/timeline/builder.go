package timeline

import (
	"fmt"
	"strings"
	"time"

	aipb "github.com/malonaz/core/genproto/ai/v1"

	"github.com/malonaz/sgpt/internal/markdown"
	"github.com/malonaz/sgpt/internal/store"
)

// ---- Builder ----

// Builder memoizes per-message item construction so a streaming refresh costs
// O(streaming message) instead of re-parsing the entire history every tick.
type Builder struct {
	messageIndexToEntry map[int]builderEntry
	toolCallIDToResult  map[string]*aipb.ToolResult
	// toolCallIDToLastGoodRequest survives item reconstruction so partial
	// tool call renders can fall back to the last successful one.
	toolCallIDToLastGoodRequest map[string]string
	scannedMessageCount         int
}

type builderEntry struct {
	// message is compared by pointer: saves replace the chat's protos, and
	// cached items would otherwise keep referencing (and mutating) stale copies.
	message *aipb.Message
	items   []Item
}

func NewBuilder() *Builder {
	return &Builder{
		messageIndexToEntry:         map[int]builderEntry{},
		toolCallIDToResult:          map[string]*aipb.ToolResult{},
		toolCallIDToLastGoodRequest: map[string]string{},
	}
}

// Build converts chat messages (plus an optional in-flight streaming message)
// into timeline items, reusing cached items for unchanged messages. Every tool
// result is paired with its originating call so request/response render
// adjacently, in call order.
func (b *Builder) Build(
	messages []*aipb.Message,
	streamingMessage *aipb.Message,
	executingToolCallID string,
	pendingToolCallIDs map[string]bool,
	requestRenderer RequestRenderer,
) []Item {
	if b.scannedMessageCount > len(messages) {
		// History shrank (chat replaced) — every cache is invalid.
		b.messageIndexToEntry = map[int]builderEntry{}
		b.toolCallIDToResult = map[string]*aipb.ToolResult{}
		b.toolCallIDToLastGoodRequest = map[string]string{}
		b.scannedMessageCount = 0
	}
	// Messages are append-only; scan only the unseen tail for tool results.
	for _, message := range messages[b.scannedMessageCount:] {
		if message.GetRole() != aipb.Role_ROLE_TOOL {
			continue
		}
		for _, block := range message.GetBlocks() {
			if toolResult := block.GetToolResult(); toolResult != nil {
				b.toolCallIDToResult[toolResult.GetToolCallId()] = toolResult
			}
		}
	}
	b.scannedMessageCount = len(messages)

	var items []Item
	for messageIndex, message := range messages {
		entry, ok := b.messageIndexToEntry[messageIndex]
		if !ok || entry.message != message {
			var messageItems []Item
			messageItems = appendMessageItems(messageItems, message, messageIndex, true, b.toolCallIDToResult, requestRenderer, b.toolCallIDToLastGoodRequest)
			if errText := store.MessageError(message); errText != "" {
				messageItems = append(messageItems, NewErrorItem(fmt.Sprintf("m%d-error", messageIndex), errText))
			}
			entry = builderEntry{message: message, items: messageItems}
			b.messageIndexToEntry[messageIndex] = entry
		}
		// Results and execution state land after an item is first built; patch
		// the cached items instead of re-parsing the whole message.
		for _, item := range entry.items {
			toolCallItem, ok := item.(*ToolCallItem)
			if !ok {
				continue
			}
			if toolCallItem.Result == nil {
				if result := toolCallItem.ToolCall.GetResult(); result != nil {
					toolCallItem.Result = result
				} else {
					toolCallItem.Result = b.toolCallIDToResult[toolCallItem.ToolCall.GetId()]
				}
			}
			toolCallID := toolCallItem.ToolCall.GetId()
			toolCallItem.Executing = executingToolCallID != "" && toolCallID == executingToolCallID
			toolCallItem.Pending = pendingToolCallIDs[toolCallID]
		}
		items = append(items, entry.items...)
	}
	if streamingMessage != nil {
		// Still mutating — never cached; finalization lands it in messages.
		items = appendMessageItems(items, streamingMessage, len(messages), false, b.toolCallIDToResult, requestRenderer, b.toolCallIDToLastGoodRequest)
	}
	return groupInjectedFiles(items)
}

// groupInjectedFiles folds runs of consecutive injected-file items into a
// single collapsible group: injecting 30 files must not cost 30 boxes of
// vertical space. Grouping happens here (not in appendMessageItems) because
// each file is its own message and items are cached per message.
func groupInjectedFiles(items []Item) []Item {
	grouped := make([]Item, 0, len(items))
	var current *InjectedFileItem
	for _, item := range items {
		fileItem, ok := item.(*InjectedFileItem)
		if !ok {
			current = nil
			grouped = append(grouped, item)
			continue
		}
		if current != nil {
			current.add(fileItem.path, fileItem.content, fileItem.MessageName())
			continue
		}
		// Copy: the cached per-message item must not accumulate siblings.
		current = NewInjectedFileItem(fileItem.id, fileItem.path, fileItem.content, fileItem.MessageName())
		grouped = append(grouped, current)
	}
	return grouped
}

// BuildChatItems is the uncached one-shot variant — used by read-only
// previews (menu detail pane). Stateful callers should hold a Builder.
func BuildChatItems(messages []*aipb.Message, requestRenderer RequestRenderer) []Item {
	return NewBuilder().Build(messages, nil, "", nil, requestRenderer)
}

func appendMessageItems(
	items []Item,
	message *aipb.Message,
	messageIndex int,
	finalized bool,
	toolCallIDToResult map[string]*aipb.ToolResult,
	requestRenderer RequestRenderer,
	toolCallIDToLastGoodRequest map[string]string,
) []Item {
	baseSeq := messageIndex * 1000
	messageName := message.GetName()
	var created time.Time
	if createTime := message.GetCreateTime(); createTime != nil {
		created = createTime.AsTime()
	}
	switch message.GetRole() {
	case aipb.Role_ROLE_USER:
		// Injected files render in-place (injection order), label-detected:
		// same message as any other, only the presentation differs.
		if path := store.InjectedFilePath(message); path != "" {
			var content string
			for _, block := range message.GetBlocks() {
				if text := block.GetText(); text != "" {
					content = text
					break
				}
			}
			items = append(items, NewInjectedFileItem(fmt.Sprintf("m%d-file", messageIndex), path, content, messageName))
			return items
		}
		// One item per user message; fences navigable within it.
		sender := store.MessageSender(message)
		var note string
		var mdBlocks []markdown.Block
		for blockIndex, block := range message.GetBlocks() {
			text := block.GetText()
			if sender != "" && blockIndex == 0 {
				note, text = agentNote(text)
			}
			if text != "" {
				mdBlocks = append(mdBlocks, markdown.ParseBlocks(text)...)
			}
		}
		textVoice := voiceUser
		if sender != "" {
			textVoice = voiceAgent
		}
		if len(mdBlocks) > 0 || sender != "" {
			items = append(items, &TextItem{
				id:          fmt.Sprintf("m%d-user", messageIndex),
				seq:         baseSeq,
				voice:       textVoice,
				blocks:      mdBlocks,
				finalized:   finalized,
				messageName: messageName,
				sender:      sender,
				note:        note,
				created:     created,
			})
		}

	case aipb.Role_ROLE_ASSISTANT:
		for blockIndex, block := range message.GetBlocks() {
			seq := baseSeq + blockIndex*20
			if thought := block.GetThought(); thought != "" {
				items = append(items, &ThoughtItem{
					id:          fmt.Sprintf("m%d-b%d-thought", messageIndex, blockIndex),
					text:        thought,
					finalized:   finalized,
					messageName: messageName,
				})
			} else if text := block.GetText(); text != "" {
				// One item per API text block; fences navigable within it.
				items = append(items, &TextItem{
					id:          fmt.Sprintf("m%d-b%d-text", messageIndex, blockIndex),
					seq:         seq,
					blocks:      markdown.ParseBlocks(text),
					finalized:   finalized,
					messageName: messageName,
					created:     created,
				})
			} else if toolCall := block.GetToolCall(); toolCall != nil {
				result := toolCall.GetResult()
				if result == nil {
					result = toolCallIDToResult[toolCall.GetId()]
				}
				items = append(items, &ToolCallItem{
					id:               fmt.Sprintf("m%d-b%d-tool", messageIndex, blockIndex),
					seq:              seq,
					ToolCall:         toolCall,
					Result:           result,
					RequestRenderer:  requestRenderer,
					lastGoodRequests: toolCallIDToLastGoodRequest,
					messageName:      messageName,
				})
			} else if partialToolCall := block.GetPartialToolCall(); partialToolCall != nil {
				// Same ID as the completed call so collapse/selection state
				// carries over when the partial upgrades to a full call.
				items = append(items, &ToolCallItem{
					id:               fmt.Sprintf("m%d-b%d-tool", messageIndex, blockIndex),
					seq:              seq,
					ToolCall:         partialToolCall,
					Partial:          true,
					RequestRenderer:  requestRenderer,
					lastGoodRequests: toolCallIDToLastGoodRequest,
					messageName:      messageName,
				})
			}
		}

	case aipb.Role_ROLE_TOOL:
		// Results are rendered inline with their calls — nothing to add.

	case aipb.Role_ROLE_SYSTEM:
		for _, block := range message.GetBlocks() {
			if text := block.GetText(); text != "" {
				items = append(items, NewSystemItem(fmt.Sprintf("m%d-system", messageIndex), baseSeq, text, messageName))
				break
			}
		}
	}
	return items
}

// agentNote splits the bracketed first line off an agent message's text.
func agentNote(text string) (note, rest string) {
	if !strings.HasPrefix(text, "[") {
		return "", text
	}
	note, rest, _ = strings.Cut(text, "\n")
	return note, strings.TrimLeft(rest, "\n")
}
