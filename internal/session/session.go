// Package session owns the chat lifecycle: streaming, tool handling and
// persistence (via the store). All methods that mutate state are blocking and
// sequential; the TUI drives the session from tea.Cmd goroutines.
//
// Messages are server-side resources: the session mirrors the chat's message
// history locally for display, sends only *new* messages (user text, tool
// results) with each generation, and lets the server persist everything.
package session

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"
	"google.golang.org/protobuf/proto"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/file"
	"github.com/malonaz/sgpt/internal/permission"
	"github.com/malonaz/sgpt/internal/review"
	"github.com/malonaz/sgpt/internal/store"
	"github.com/malonaz/sgpt/internal/tool"
)

// State is the session lifecycle phase; the UI derives what to show
// (input, spinner, review hints) from it rather than from ad-hoc booleans.
type State int

const (
	StateIdle State = iota
	StateStreaming
	StateExecutingTools
	StateAwaitingReview
)

// sessionIDs hands out the identities sessions own their reviews by; chat
// names are assigned lazily and so cannot serve.
var sessionIDs atomic.Uint64

// Params bundles per-chat parameters for a session.
type Params struct {
	Model           *aipb.Model
	Role            *sgptpb.Role
	MaxTokens       int32
	Temperature     float64
	ReasoningEffort aipb.ReasoningEffort
	Tools           []string
	// AvailableToolNames lists every selectable tool name (builtin or tool
	// engine config name); Tools is only the initial selection.
	AvailableToolNames []string
	// ResolveTool expands a user-facing tool name into the advertised
	// tool/tool-set names, lazily initializing tool engines on first use.
	ResolveTool func(ctx context.Context, name string) ([]string, error)
	Chat        string
	// SystemPrompt is persisted as a ROLE_SYSTEM message on the first turn.
	SystemPrompt  string
	InjectedFiles []string
	// InjectedFileContents maps an injected path to preloaded content
	// (virtual injections, e.g. knowledge-graph nodes). Paths present here
	// are used verbatim — never normalized, never read from disk.
	InjectedFileContents map[string]string
	// LoreNameForPath recognizes an injected file as a lore, returning its
	// canonical name. Lores enter the context as plain files, so this is the
	// only way to tell them apart when reporting what the context holds.
	LoreNameForPath func(path string) (string, bool)
	// Policy decides how the session's tool calls are handled; nil means
	// each tool's own review policy. Sub-agents receive a child of their
	// launcher's policy.
	Policy *permission.Policy
	// Depth is 0 for a user-opened chat, one more per sub-agent level.
	Depth int
	// Reviews is the launcher's review queue, for a sub-agent: its calls
	// await the user's verdict in the root chat. Nil starts a new tree.
	Reviews *review.Queue
	// Supervisor is the sub-agent system, told when the session cancels a
	// turn or closes; nil when sub-agents are not configured.
	Supervisor tool.Supervisor
	// MaxAgentTurns caps the turns messages from other agents may start
	// before the user speaks in the chat again; 0 is unlimited.
	MaxAgentTurns int
}

// Session drives a single chat conversation.
type Session struct {
	ctx      context.Context
	params   Params
	store    *store.Store
	registry *tool.Registry

	mu sync.Mutex
	// chat is replaced, never mutated in place: callers read the snapshots
	// Chat returns without the lock.
	chat *aipb.Chat
	// chatRevision counts local chat edits, so a write never adopts a
	// response that predates one.
	chatRevision uint64
	// messages is the local mirror of the chat's server-side message
	// history, oldest first. Input messages appended locally may lack a
	// resource name (the server persists them without echoing them back).
	messages          []*aipb.Message
	streamingMessage  *aipb.Message
	streamError       error
	executingToolCall string
	// currentTurn is the turn in flight, nil when idle. It scopes one full
	// exchange (context RPCs, streams, tool loops); cancelling it aborts the
	// turn wherever it is — including before the first stream opens.
	currentTurn *turn
	// titleGenerating guards against launching concurrent title generations.
	titleGenerating bool

	// policy decides whether a tool call runs, awaits the user or is
	// refused; "always accept" is a grant on it.
	policy *permission.Policy

	// id identifies this session's requests in the review queue.
	id uint64
	// reviews holds the tool calls awaiting a verdict across the chat tree.
	// This *is* the review state: a reviewed call executes immediately and
	// its result is terminal.
	reviews *review.Queue
	// reviewsRoot marks the chat that started the tree: it shows every
	// pending review, its sub-agents' included.
	reviewsRoot bool

	// injectedFilePaths are the files in the model context, mutable at
	// runtime via SetInjectedFiles. Each path is persisted exactly once as a
	// labeled user message; injectedFilePathToMessageName tracks which paths
	// already live in the chat so re-toggling never re-sends content and the
	// provider prompt cache stays stable.
	injectedFilePaths             []string
	injectedFilePathToMessageName map[string]string
	// systemPromptSent guards the one-time persistence of the system message.
	systemPromptSent bool

	// pendingInputMessages queues the new messages (user text, tool results)
	// consumed by the next generation request.
	pendingInputMessages []*aipb.Message

	// queuedMessages holds user interjections typed while a turn is running;
	// they are appended (after any tool results) to the next generation's
	// input, or carried into the next turn if the current one ends first.
	queuedMessages []*aipb.Message

	// enabledUserToolNameSet is the user-facing tool selection;
	// enabledAdvertisedNameSet is its expansion to the tool/tool-set names
	// actually advertised to the model.
	enabledUserToolNameSet   map[string]bool
	enabledAdvertisedNameSet map[string]bool

	// lastModelUsage is the in-flight generation's cumulative usage; chat-wide
	// usage and price are server-maintained on s.chat.
	lastModelUsage *aipb.ModelUsage

	// pendingErrors queues non-fatal errors for the TUI. Errors are never
	// dropped, so they are held here rather than in the lossy event channel.
	pendingErrors []error

	// replyWaiters are told how the turn consuming a queued message ended;
	// they move onto that turn when it takes the message.
	replyWaiters map[*aipb.Message][]func(tool.TurnOutcome)
	// inboxCh is closed, and replaced, whenever a message is queued.
	inboxCh chan struct{}
	// agentTurns counts the turns agent messages started since the user last
	// sent one, bounded by params.MaxAgentTurns.
	agentTurns int
	// closed marks a session whose tab is gone: nothing may start its turns.
	closed bool

	// renderThrottle paces RefreshEvents: token streams emit far faster than
	// a terminal can usefully render.
	renderThrottle throttle

	eventCh chan Event
}

func New(
	ctx context.Context,
	chatStore *store.Store,
	registry *tool.Registry,
	chat *aipb.Chat,
	messages []*aipb.Message,
	params Params,
) *Session {
	policy := params.Policy
	if policy == nil {
		policy = &permission.Policy{}
	}
	s := &Session{
		ctx:                           ctx,
		params:                        params,
		store:                         chatStore,
		registry:                      registry,
		chat:                          chat,
		messages:                      messages,
		policy:                        policy,
		injectedFilePathToMessageName: map[string]string{},
		replyWaiters:                  map[*aipb.Message][]func(tool.TurnOutcome){},
		inboxCh:                       make(chan struct{}),
		lastModelUsage:                &aipb.ModelUsage{},
		renderThrottle:                throttle{interval: renderInterval},
		eventCh:                       make(chan Event, 64),
	}
	// Tools execute against this session's history and identity (the
	// registry is one instance shared across main chat and sub-agents):
	// stamped on the context so tools can derive what the model has already
	// seen, and what a sub-agent may inherit.
	s.initReviews(params.Reviews)
	s.ctx = tool.WithHistory(s.ctx, s.historyForTools)
	s.ctx = tool.WithCaller(s.ctx, s.caller)
	s.injectedFilePaths = s.normalizeInjectedPaths(params.InjectedFiles)
	// Sort once (on a copy — the slice is shared across sessions via the
	// app's default params): the tool picker reads this on every open.
	s.params.AvailableToolNames = append([]string(nil), params.AvailableToolNames...)
	sort.Strings(s.params.AvailableToolNames)
	// Resuming a chat: context messages are already persisted server-side.
	for _, message := range messages {
		if path := store.InjectedFilePath(message); path != "" && message.GetDeleteTime() == nil {
			s.injectedFilePathToMessageName[path] = message.GetName()
		}
		if message.GetRole() == aipb.Role_ROLE_SYSTEM {
			s.systemPromptSent = true
		}
	}
	s.enabledUserToolNameSet = map[string]bool{}
	s.enabledAdvertisedNameSet = map[string]bool{}
	// Seed from --tool/role config. The CLI already resolved these names at
	// launch (dialing their engines), so this is served from cache.
	// A failure here silently shrinks the toolset, so surface it as an alert.
	if err := s.SetEnabledTools(params.Tools); err != nil {
		s.pendingErrors = append(s.pendingErrors, fmt.Errorf("enabling configured tools: %w", err))
	}
	// Render the context (system prompt, injected files) immediately: it is
	// only persisted on the first turn, but the user should see what the
	// model will see the moment the chat opens.
	s.seedOptimisticContext()
	return s
}

// RepairHistory resolves tool calls left unanswered by a previous run so the
// chat can be resumed at all.
//
// A provider rejects the whole conversation when an assistant `tool_use`
// block has no matching `tool_result` (Anthropic: 400 "tool_use ids were
// found without tool_result blocks"), which makes the chat permanently
// unusable — every turn fails before reaching the model. The gap appears
// whenever a turn dies after the assistant message was persisted but before
// its tool message was: a crash, a kill, or a tab closed mid-review.
//
// Synthesizing an error result per orphan is the only recovery that keeps the
// history valid *and* honest: the model is told the call did not run, instead
// of being fed a fabricated success.
//
// Blocking (one RPC) — call it off the UI loop. Idempotent: a repaired
// history has no orphans left.
func (s *Session) RepairHistory() error {
	s.mu.Lock()
	orphanedToolCalls := store.OrphanedToolCalls(s.messages)
	s.mu.Unlock()
	if len(orphanedToolCalls) == 0 {
		return nil
	}

	resultBlocks := make([]*aipb.Block, 0, len(orphanedToolCalls))
	for _, toolCall := range orphanedToolCalls {
		toolResult := toolCall.GetResult()
		if toolResult == nil {
			toolResult = ai.NewErrorToolResult(toolCall.GetName(), toolCall.GetId(),
				fmt.Errorf("interrupted: this tool call never ran"))
		}
		resultBlocks = append(resultBlocks, ai.NewToolResultBlock(toolResult))
	}

	toolMessage := ai.NewToolMessage(resultBlocks...)
	// Persisted eagerly rather than queued as pending input: the gap is a
	// property of the stored history, so leaving the repair unsent would let
	// the very next resume hit the same wall.
	createdMessage, err := s.store.CreateMessage(s.ctx, s.Chat().GetName(), toolMessage)
	if err != nil {
		return fmt.Errorf("repairing %d unanswered tool call(s): %w", len(orphanedToolCalls), err)
	}

	s.mu.Lock()
	s.messages = append(s.messages, createdMessage)
	s.mu.Unlock()
	s.refresh()
	return nil
}

// seedOptimisticContext mirrors the not-yet-persisted context messages into
// the local history so they render before the first turn. ensureContext
// replaces these placeholders with the server-persisted messages.
func (s *Session) seedOptimisticContext() {
	if s.params.SystemPrompt != "" && !s.systemPromptSent {
		s.messages = append(s.messages, ai.NewSystemMessage(ai.NewTextBlock(s.params.SystemPrompt)))
	}
	for _, path := range s.injectedFilePaths {
		if _, ok := s.injectedFilePathToMessageName[path]; ok {
			continue
		}
		s.messages = append(s.messages, store.NewInjectedFileMessage(path, s.injectedFileContent(path)))
	}
}

// injectedFileContent formats a file for injection; a vanished file degrades
// to an inline note instead of killing the turn.
func (s *Session) injectedFileContent(path string) string {
	// Virtual injections carry their content; there is no file behind them.
	if content, ok := s.params.InjectedFileContents[path]; ok {
		return content
	}
	injectedFile, err := file.Read(path)
	if err != nil {
		return fmt.Sprintf("file %s: [unreadable: %v]", path, err)
	}
	return fmt.Sprintf("file %s: `%s`", path, injectedFile.Content)
}

// normalizeInjectedPaths normalizes real file paths but keeps virtual
// (content-overridden) paths verbatim, so they keep matching their override.
func (s *Session) normalizeInjectedPaths(paths []string) []string {
	normalized := make([]string, 0, len(paths))
	seen := map[string]bool{}
	for _, path := range paths {
		if _, ok := s.params.InjectedFileContents[path]; !ok {
			path = file.Normalize([]string{path})[0]
		}
		if !seen[path] {
			seen[path] = true
			normalized = append(normalized, path)
		}
	}
	return normalized
}

func (s *Session) Events() <-chan Event {
	return s.eventCh
}

func (s *Session) emit(event Event) {
	select {
	case s.eventCh <- event:
	default:
	}
}

func (s *Session) refresh() {
	s.renderThrottle.call(func() { s.emit(RefreshEvent{}) })
}

func (s *Session) emitError(err error) {
	// Errors are queued, never dropped and never blocking: refreshes are
	// coalescable (a lost one is harmless, another always follows) but an
	// error is the only record of a failure.
	s.mu.Lock()
	s.pendingErrors = append(s.pendingErrors, err)
	s.mu.Unlock()
	s.emit(errorPendingEvent{})
}

// takePendingErrors drains the queued errors for the TUI to surface.
func (s *Session) takePendingErrors() []error {
	s.mu.Lock()
	defer s.mu.Unlock()
	errors := s.pendingErrors
	s.pendingErrors = nil
	return errors
}

func (s *Session) Chat() *aipb.Chat {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.chat
}

// Messages returns the local mirror of the chat's message history.
func (s *Session) Messages() []*aipb.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*aipb.Message(nil), s.messages...)
}

// caller is the identity tools see while executing: what the agent tool may
// hand down to a sub-agent.
func (s *Session) caller() tool.Caller {
	s.mu.Lock()
	defer s.mu.Unlock()
	tools := make([]string, 0, len(s.enabledUserToolNameSet))
	for name := range s.enabledUserToolNameSet {
		tools = append(tools, name)
	}
	sort.Strings(tools)
	return tool.Caller{
		Chat:            s.chat.GetName(),
		Depth:           s.params.Depth,
		Model:           s.params.Model,
		ReasoningEffort: s.params.ReasoningEffort,
		Tools:           tools,
		Policy:          s.policy,
		Reviews:         s.reviews,
		Agent:           s,
	}
}

// historyForTools is the history tools see while executing: the committed
// messages plus the in-flight streaming message, so a tool observes results
// produced earlier in the same turn.
func (s *Session) historyForTools() []*aipb.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	messages := append([]*aipb.Message(nil), s.messages...)
	if s.streamingMessage != nil {
		messages = append(messages, s.streamingMessage)
	}
	return messages
}

func (s *Session) StreamingMessage() *aipb.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.streamingMessage
}

func (s *Session) StreamError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.streamError
}

// State derives the lifecycle phase from the session's facts — nothing ever
// stores it, so it can never go stale. A pending review outranks execution,
// which outranks a turn merely being in flight.
func (s *Session) State() State {
	awaitingReview := len(s.visibleReviews()) > 0
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case awaitingReview:
		return StateAwaitingReview
	case s.executingToolCall != "":
		return StateExecutingTools
	case s.currentTurn != nil:
		return StateStreaming
	default:
		return StateIdle
	}
}

func (s *Session) IsStreaming() bool {
	return s.State() == StateStreaming
}

// Busy reports whether the session is actively working (input disabled).
func (s *Session) Busy() bool {
	state := s.State()
	return state == StateStreaming || state == StateExecutingTools
}

// ExecutingToolCallID identifies the tool call currently in flight, if any.
func (s *Session) ExecutingToolCallID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.executingToolCall
}

func (s *Session) setExecutingToolCall(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.executingToolCall = id
}

func (s *Session) Params() Params {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.params
}

// TotalModelUsage folds the chat's per-model usage into one aggregate. The
// server rolls usage onto the chat after every generation; it is adopted here
// on the next chat write (see turn.run), so this trails a stream by one flush.
func (s *Session) TotalModelUsage() *aipb.ModelUsage {
	s.mu.Lock()
	defer s.mu.Unlock()
	total := &aipb.ModelUsage{}
	ai.AggregateModelUsage(total, s.chat.GetModelUsages()...)
	return total
}

func (s *Session) LastModelUsage() *aipb.ModelUsage {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !ai.IsModelUsageEmpty(s.lastModelUsage) {
		return s.lastModelUsage
	}
	// No stream in flight (or chat freshly loaded): fall back to the most
	// recent persisted message that carries usage, so context % stays accurate.
	for i := len(s.messages) - 1; i >= 0; i-- {
		if usage := s.messages[i].GetModelUsage(); !ai.IsModelUsageEmpty(usage) {
			return usage
		}
	}
	return s.lastModelUsage
}

// Price is the server-maintained, append-only spend of the chat.
func (s *Session) Price() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.chat.GetPrice()
}

// reasoningEfforts is the cycle order: unset (the model's own behavior),
// then rising effort.
var reasoningEfforts = []aipb.ReasoningEffort{
	aipb.ReasoningEffort_REASONING_EFFORT_UNSPECIFIED,
	aipb.ReasoningEffort_REASONING_EFFORT_LOW,
	aipb.ReasoningEffort_REASONING_EFFORT_MEDIUM,
	aipb.ReasoningEffort_REASONING_EFFORT_HIGH,
	aipb.ReasoningEffort_REASONING_EFFORT_XHIGH,
	aipb.ReasoningEffort_REASONING_EFFORT_MAX,
}

// CycleReasoningEffort steps to the next reasoning effort, wrapping to unset.
func (s *Session) CycleReasoningEffort() {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := slices.Index(reasoningEfforts, s.params.ReasoningEffort) + 1
	s.params.ReasoningEffort = reasoningEfforts[next%len(reasoningEfforts)]
}

// ReasoningLabel names an effort as the UI shows it ("xhigh"); empty when
// unset.
func ReasoningLabel(effort aipb.ReasoningEffort) string {
	if effort == aipb.ReasoningEffort_REASONING_EFFORT_UNSPECIFIED {
		return ""
	}
	return strings.ToLower(strings.TrimPrefix(effort.String(), "REASONING_EFFORT_"))
}

// InjectedFiles returns the file paths currently injected into the context.
func (s *Session) InjectedFiles() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.injectedFilePaths...)
}

// SetInjectedFiles replaces the injected files. Removed paths have their
// messages soft-deleted (the server drops them from provider history); added
// paths are persisted on the next turn, appended to the history like any
// other message — no chat update, so the provider prompt cache stays intact.
// Blocking — call it off the UI loop.
func (s *Session) SetInjectedFiles(paths []string) {
	paths = s.normalizeInjectedPaths(paths)

	s.mu.Lock()
	removedPathToMessageName := map[string]string{}
	pathSet := make(map[string]bool, len(paths))
	for _, path := range paths {
		pathSet[path] = true
	}
	for path, messageName := range s.injectedFilePathToMessageName {
		if !pathSet[path] {
			removedPathToMessageName[path] = messageName
		}
	}
	// Drop optimistic (never persisted) file messages outright — there is
	// nothing server-side to soft-delete.
	for _, path := range s.injectedFilePaths {
		if pathSet[path] {
			continue
		}
		if index := s.optimisticInjectedFileIndex(path); index >= 0 {
			s.messages = append(s.messages[:index], s.messages[index+1:]...)
		}
	}
	s.injectedFilePaths = append([]string(nil), paths...)
	// Newly added paths render immediately; ensureContext persists them on
	// the next turn and swaps in the server copy.
	for _, path := range paths {
		if _, ok := s.injectedFilePathToMessageName[path]; ok {
			continue
		}
		if s.optimisticInjectedFileIndex(path) >= 0 {
			continue
		}
		s.messages = append(s.messages, store.NewInjectedFileMessage(path, s.injectedFileContent(path)))
	}
	s.mu.Unlock()

	for path, messageName := range removedPathToMessageName {
		if messageName == "" {
			continue
		}
		if err := s.store.DeleteMessage(s.ctx, messageName); err != nil {
			s.emitError(fmt.Errorf("removing injected file %s: %w", path, err))
			continue
		}
		s.mu.Lock()
		delete(s.injectedFilePathToMessageName, path)
		for i, message := range s.messages {
			if message.GetName() == messageName {
				s.messages = append(s.messages[:i], s.messages[i+1:]...)
				break
			}
		}
		s.mu.Unlock()
	}

	s.refresh()
}

// optimisticInjectedFileIndex locates a not-yet-persisted injected-file
// message for path (no resource name yet). Caller holds the lock.
func (s *Session) optimisticInjectedFileIndex(path string) int {
	for i, message := range s.messages {
		if message.GetName() == "" && store.InjectedFilePath(message) == path {
			return i
		}
	}
	return -1
}

// DeleteMessage soft-deletes a message and drops it from the local history.
// The server excludes deleted messages from the provider history, so this is
// the escape hatch for a message that breaks generation (an oversized paste, a
// poisoned tool result).
//
// Deleting an assistant message that carries tool calls would orphan them, so
// the paired tool results are deleted with it — a lone `tool_use` block makes
// the provider reject the entire conversation.
//
// Blocking (one RPC per message) — call it off the UI loop.
func (s *Session) DeleteMessage(messageName string) error {
	if messageName == "" {
		// Optimistic, never-persisted messages (queued context) have no
		// server-side resource to delete.
		return fmt.Errorf("message is not persisted yet")
	}

	s.mu.Lock()
	messageNamesToDelete := append([]string{messageName}, s.pairedMessageNames(messageName)...)
	s.mu.Unlock()
	return s.deleteMessages(messageNamesToDelete)
}

// DeleteMessagesFrom soft-deletes messageName and every persisted message
// below it, truncating the conversation back to the point just above so it can
// be continued from there. Blocking — call it off the UI loop.
//
// Anchoring on a tool result would orphan the assistant tool call above it,
// which makes the provider reject the whole chat — so the cut widens up to the
// message that issued the calls.
func (s *Session) DeleteMessagesFrom(messageName string) (int, error) {
	if messageName == "" {
		return 0, fmt.Errorf("message is not persisted yet")
	}

	s.mu.Lock()
	start := -1
	for i, message := range s.messages {
		if message.GetName() == messageName {
			start = i
			break
		}
	}
	if start == -1 {
		s.mu.Unlock()
		return 0, fmt.Errorf("message not found in history")
	}
	if owner := s.toolCallOwnerIndex(start); owner != -1 {
		start = owner
	}
	var messageNamesToDelete []string
	for _, message := range s.messages[start:] {
		// Optimistic messages have no server-side resource yet.
		if name := message.GetName(); name != "" {
			messageNamesToDelete = append(messageNamesToDelete, name)
		}
	}
	s.mu.Unlock()
	return len(messageNamesToDelete), s.deleteMessages(messageNamesToDelete)
}

// toolCallOwnerIndex returns the index of the assistant message whose tool
// calls the message at index answers, or -1 when it carries no tool results.
// Caller holds the lock.
func (s *Session) toolCallOwnerIndex(index int) int {
	toolCallIDSet := map[string]bool{}
	for _, block := range ai.FilterBlocks(s.messages[index].GetBlocks(), ai.BlockTypeToolResult) {
		toolCallIDSet[block.GetToolResult().GetToolCallId()] = true
	}
	if len(toolCallIDSet) == 0 {
		return -1
	}
	for i := index - 1; i >= 0; i-- {
		for _, block := range ai.FilterBlocks(s.messages[i].GetBlocks(), ai.BlockTypeToolCall) {
			if toolCallIDSet[block.GetToolCall().GetId()] {
				return i
			}
		}
	}
	return -1
}

// deleteMessages soft-deletes names on the server, then drops them from the
// local history. On a mid-way failure the messages already deleted server-side
// are still dropped locally, so the view never shows ghosts.
func (s *Session) deleteMessages(messageNamesToDelete []string) error {
	if len(messageNamesToDelete) == 0 {
		return nil
	}
	deletedNameSet := make(map[string]bool, len(messageNamesToDelete))
	var deleteErr error
	for _, name := range messageNamesToDelete {
		if err := s.store.DeleteMessage(s.ctx, name); err != nil {
			deleteErr = fmt.Errorf("deleting message: %w", err)
			break
		}
		deletedNameSet[name] = true
	}

	s.mu.Lock()
	remainingMessages := make([]*aipb.Message, 0, len(s.messages))
	for _, message := range s.messages {
		if deletedNameSet[message.GetName()] {
			// Keep the injected-file bookkeeping in step, so re-selecting the
			// path re-injects it instead of silently doing nothing.
			if path := store.InjectedFilePath(message); path != "" {
				delete(s.injectedFilePathToMessageName, path)
				s.injectedFilePaths = removePath(s.injectedFilePaths, path)
			}
			continue
		}
		remainingMessages = append(remainingMessages, message)
	}
	s.messages = remainingMessages
	s.mu.Unlock()
	s.refresh()
	return deleteErr
}

// removePath drops the first occurrence of path from paths.
func removePath(paths []string, path string) []string {
	for i, candidate := range paths {
		if candidate == path {
			return append(paths[:i], paths[i+1:]...)
		}
	}
	return paths
}

// pairedMessageNames returns the messages that must be deleted alongside
// messageName to keep the history valid: the tool messages answering its tool
// calls. Caller holds the lock.
func (s *Session) pairedMessageNames(messageName string) []string {
	toolCallIDSet := map[string]bool{}
	for _, message := range s.messages {
		if message.GetName() != messageName {
			continue
		}
		for _, block := range ai.FilterBlocks(message.GetBlocks(), ai.BlockTypeToolCall) {
			toolCallIDSet[block.GetToolCall().GetId()] = true
		}
		break
	}
	if len(toolCallIDSet) == 0 {
		return nil
	}

	var pairedNames []string
	for _, message := range s.messages {
		if message.GetName() == "" || message.GetName() == messageName {
			continue
		}
		for _, block := range ai.FilterBlocks(message.GetBlocks(), ai.BlockTypeToolResult) {
			if toolCallIDSet[block.GetToolResult().GetToolCallId()] {
				pairedNames = append(pairedNames, message.GetName())
				break
			}
		}
	}
	return pairedNames
}

// optimisticSystemPromptIndex locates the not-yet-persisted system message.
// Caller holds the lock.
func (s *Session) optimisticSystemPromptIndex() int {
	for i, message := range s.messages {
		if message.GetName() == "" && message.GetRole() == aipb.Role_ROLE_SYSTEM {
			return i
		}
	}
	return -1
}

// ensureChat lazily creates the server-side chat on the first turn, so a tab
// that is opened and abandoned never leaves an empty chat behind. Local
// mutations made before the first send (labels, title, favorite) ride along
// in the create payload. Idempotent.
func (s *Session) ensureChat() error {
	s.mu.Lock()
	chat := s.chat
	s.mu.Unlock()
	if chat.GetName() != "" {
		return nil
	}
	createdChat, err := s.store.CreateChat(s.turnContext(), chat)
	if err != nil {
		return fmt.Errorf("creating chat: %w", err)
	}
	s.mu.Lock()
	s.chat = createdChat
	s.params.Chat = createdChat.GetName()
	s.mu.Unlock()
	return nil
}

// ensureContext persists the one-time context messages (system prompt, newly
// injected files) before a generation. Idempotent.
func (s *Session) ensureContext() error {
	if err := s.ensureChat(); err != nil {
		return err
	}
	chatName := s.Chat().GetName()

	s.mu.Lock()
	systemPrompt := s.params.SystemPrompt
	systemPromptPending := systemPrompt != "" && !s.systemPromptSent
	var newFilePaths []string
	for _, path := range s.injectedFilePaths {
		if _, ok := s.injectedFilePathToMessageName[path]; !ok {
			newFilePaths = append(newFilePaths, path)
		}
	}
	s.mu.Unlock()

	if systemPromptPending {
		systemMessage := ai.NewSystemMessage(ai.NewTextBlock(systemPrompt))
		createdMessage, err := s.store.CreateMessage(s.turnContext(), chatName, systemMessage)
		if err != nil {
			return fmt.Errorf("persisting system prompt: %w", err)
		}
		s.mu.Lock()
		s.systemPromptSent = true
		// Swap the optimistic placeholder for the persisted message so the
		// timeline position (and its item cache) stays stable.
		if index := s.optimisticSystemPromptIndex(); index >= 0 {
			s.messages[index] = createdMessage
		} else {
			s.messages = append(s.messages, createdMessage)
		}
		s.mu.Unlock()
	}

	for _, path := range newFilePaths {
		createdMessage, err := s.store.CreateMessage(s.turnContext(), chatName, store.NewInjectedFileMessage(path, s.injectedFileContent(path)))
		if err != nil {
			return fmt.Errorf("persisting injected file %s: %w", path, err)
		}
		s.mu.Lock()
		s.injectedFilePathToMessageName[path] = createdMessage.GetName()
		if index := s.optimisticInjectedFileIndex(path); index >= 0 {
			s.messages[index] = createdMessage
		} else {
			s.messages = append(s.messages, createdMessage)
		}
		s.mu.Unlock()
	}
	return nil
}

// AvailableToolNames lists every selectable tool name, sorted (in New).
func (s *Session) AvailableToolNames() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.params.AvailableToolNames...)
}

// EnabledTools returns the user-facing tool names currently enabled.
func (s *Session) EnabledTools() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	enabled := make(map[string]bool, len(s.enabledUserToolNameSet))
	for name := range s.enabledUserToolNameSet {
		enabled[name] = true
	}
	return enabled
}

// SetEnabledTools replaces the enabled tool selection (user-facing names);
// takes effect on the next request. May block dialing a not-yet-initialized
// tool engine — call it off the UI loop. Resolution happens outside the
// session lock so renders never stall behind a dial.
func (s *Session) SetEnabledTools(names []string) error {
	// ResolveTool is set once in New and never mutated, so reading it without
	// the lock is safe — and required, since resolution may dial.
	resolve := s.params.ResolveTool
	if resolve == nil {
		// No resolver configured: names are advertised as-is.
		resolve = func(_ context.Context, name string) ([]string, error) { return []string{name}, nil }
	}
	userSet := make(map[string]bool, len(names))
	advertisedSet := map[string]bool{}
	for _, name := range names {
		advertisedNames, err := resolve(s.ctx, name)
		if err != nil {
			return fmt.Errorf("enabling tool %q: %w", name, err)
		}
		userSet[name] = true
		for _, advertisedName := range advertisedNames {
			advertisedSet[advertisedName] = true
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enabledUserToolNameSet = userSet
	s.enabledAdvertisedNameSet = advertisedSet
	// Keep params in sync so the titlebar reflects the new selection.
	s.params.Tools = append([]string(nil), names...)
	return nil
}

func (s *Session) toolEnabled(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enabledAdvertisedNameSet[name]
}

// advertisedTools filters the registry's tool definitions by the enabled set.
func (s *Session) advertisedTools() []*aipb.Tool {
	var tools []*aipb.Tool
	for _, advertisedTool := range s.registry.Tools() {
		if s.toolEnabled(advertisedTool.GetName()) {
			tools = append(tools, advertisedTool)
		}
	}
	return tools
}

// advertisedToolSets filters the registry's tool sets by the enabled set.
func (s *Session) advertisedToolSets() []*aipb.ToolSet {
	var toolSets []*aipb.ToolSet
	for _, toolSet := range s.registry.ToolSets() {
		if s.toolEnabled(toolSet.GetName()) {
			toolSets = append(toolSets, toolSet)
		}
	}
	return toolSets
}

// lastAssistantText joins the text blocks of the most recent assistant message.
func (s *Session) lastAssistantText() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.messages) - 1; i >= 0; i-- {
		message := s.messages[i]
		if message.GetRole() != aipb.Role_ROLE_ASSISTANT {
			continue
		}
		var parts []string
		for _, block := range ai.FilterBlocks(message.GetBlocks(), ai.BlockTypeText) {
			parts = append(parts, block.GetText())
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// isTypedByUser tells the user's own words from sgpt's context and other
// agents' messages, which share the user role.
func isTypedByUser(message *aipb.Message) bool {
	return !store.IsContextMessage(message) && store.MessageSender(message) == ""
}

// UserMessageText returns the text of the user-typed message named
// messageName, or "" when it's anything else. Injected context (system prompt,
// files) is also ROLE_USER but not something to re-edit.
func (s *Session) UserMessageText(messageName string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, message := range s.messages {
		if message.GetName() != messageName || message.GetRole() != aipb.Role_ROLE_USER || !isTypedByUser(message) {
			continue
		}
		var parts []string
		for _, block := range ai.FilterBlocks(message.GetBlocks(), ai.BlockTypeText) {
			parts = append(parts, block.GetText())
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// SendMessage sends the user's message: an interjection while a turn runs,
// otherwise the start of a turn it runs to completion. Blocking.
func (s *Session) SendMessage(text string) {
	nextTurn, _ := s.enqueue(ai.NewUserMessage(ai.NewTextBlock(text)), nil, false)
	s.runTurns(nextTurn)
}

// Deliver hands the session a message from another agent; see tool.Agent.
// Unlike SendMessage it never blocks: a turn it starts runs on its own
// goroutine.
func (s *Session) Deliver(message *aipb.Message) (<-chan tool.TurnOutcome, error) {
	outcomeCh := make(chan tool.TurnOutcome, 1)
	nextTurn, err := s.enqueue(message, func(outcome tool.TurnOutcome) { outcomeCh <- outcome }, true)
	if err != nil {
		return nil, err
	}
	go s.runTurns(nextTurn)
	return outcomeCh, nil
}

// enqueue queues a message for the next generation and arms a turn for it
// when none is running. waiter, if set, learns how the consuming turn ends.
// The queue-or-start decision and the turn arming share one lock
// acquisition, so a message can never race a turn start.
func (s *Session) enqueue(message *aipb.Message, waiter func(tool.TurnOutcome), fromAgent bool) (*turn, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, tool.ErrAgentClosed
	}
	// Queued — and rendered as queued — until a generation consumes it, after
	// any tool results so the history stays valid.
	s.queuedMessages = append(s.queuedMessages, message)
	if waiter != nil {
		s.replyWaiters[message] = append(s.replyWaiters[message], waiter)
	}
	close(s.inboxCh)
	s.inboxCh = make(chan struct{})
	if !fromAgent {
		s.agentTurns = 0
	}
	if s.currentTurn != nil {
		s.mu.Unlock()
		s.refresh()
		return nil, nil
	}
	if fromAgent && s.params.MaxAgentTurns > 0 && s.agentTurns >= s.params.MaxAgentTurns {
		// Kept queued: the user's next message carries it to the model.
		delete(s.replyWaiters, message)
		s.mu.Unlock()
		s.emitError(fmt.Errorf("%s: %w", s.Chat().GetTitle(), tool.ErrAgentTurnLimit))
		s.refresh()
		return nil, tool.ErrAgentTurnLimit
	}
	if fromAgent {
		s.agentTurns++
	}
	nextTurn := s.startTurnLocked()
	s.mu.Unlock()
	return nextTurn, nil
}

// runTurns runs an armed turn, and every turn that follows it, to the end.
func (s *Session) runTurns(nextTurn *turn) {
	for nextTurn != nil {
		nextTurn = s.runTurn(nextTurn)
	}
}

// takeWaitersLocked detaches the reply waiters of messages a turn consumes.
// Caller holds the lock.
func (s *Session) takeWaitersLocked(messages []*aipb.Message) []func(tool.TurnOutcome) {
	var waiters []func(tool.TurnOutcome)
	for _, message := range messages {
		waiters = append(waiters, s.replyWaiters[message]...)
		delete(s.replyWaiters, message)
	}
	return waiters
}

// failQueuedWaiters resolves the waiters of messages still queued: the turn
// that would have answered them ended without doing so. The messages stay
// queued for the next turn.
func (s *Session) failQueuedWaiters(err error) {
	s.mu.Lock()
	waiters := s.takeWaitersLocked(s.queuedMessages)
	s.mu.Unlock()
	for _, waiter := range waiters {
		waiter(tool.TurnOutcome{Err: err})
	}
}

// Inbox returns a channel closed once a message is queued; see tool.Agent.
func (s *Session) Inbox() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queuedMessages) > 0 {
		return closedChannel
	}
	return s.inboxCh
}

var closedChannel = func() chan struct{} {
	channel := make(chan struct{})
	close(channel)
	return channel
}()

// Close retires the session with its tab: the running turn stops, pending
// replies fail, and messages from other agents are refused from now on.
func (s *Session) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.CancelTurn()
	s.failQueuedWaiters(tool.ErrAgentClosed)
}

// startTurnLocked arms a turn for the queued messages; interjections a
// cancelled turn left behind keep their place ahead of the newer message.
// Caller holds the lock.
func (s *Session) startTurnLocked() *turn {
	t := newTurn(s)
	t.inputs = s.queuedMessages
	t.waiters = s.takeWaitersLocked(t.inputs)
	s.queuedMessages = nil
	s.currentTurn = t
	// Optimistic render: the user's message must appear immediately. The
	// context RPCs (system prompt, injected files) are a network round trip
	// that would otherwise leave the timeline empty.
	s.streamError = nil
	s.messages = append(s.messages, t.inputs...)
	s.pendingInputMessages = append(s.pendingInputMessages, t.inputs...)
	return t
}

// runTurn runs an armed turn to its end and returns the turn that follows
// it, if any.
func (s *Session) runTurn(t *turn) *turn {
	s.refresh()
	err := s.ensureContext()
	if err != nil {
		s.unsendInputs(t)
		s.emitError(err)
		t.complete("", err)
	} else {
		s.refresh()
		err = t.run()
	}
	return s.endTurn(t, err)
}

// unsendInputs returns the inputs of a turn that never reached the model to
// the queue, ahead of anything typed since: shown as sent they would be a lie,
// and dropped the user's words would be lost.
func (s *Session) unsendInputs(t *turn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	isInput := func(message *aipb.Message) bool { return slices.Contains(t.inputs, message) }
	s.messages = slices.DeleteFunc(s.messages, isInput)
	s.pendingInputMessages = slices.DeleteFunc(s.pendingInputMessages, isInput)
	s.queuedMessages = slices.Concat(t.inputs, s.queuedMessages)
}

// endTurn releases the turn; the session is idle again and the next
// SendMessage starts fresh. A completed turn hands over to a new one when
// interjections are still queued: they arrived after its last generation,
// and nothing else would ever send them.
func (s *Session) endTurn(t *turn, err error) *turn {
	var nextTurn *turn
	s.mu.Lock()
	if s.currentTurn == t {
		s.currentTurn = nil
		if err == nil && len(s.queuedMessages) > 0 && !s.closed {
			nextTurn = s.startTurnLocked()
		}
	}
	s.mu.Unlock()
	t.cancel()
	if err != nil {
		s.failQueuedWaiters(err)
	}
	s.refresh()
	return nextTurn
}

// turnContext returns the running turn's context, falling back to the
// session context outside a turn.
func (s *Session) turnContext() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.currentTurn != nil {
		return s.currentTurn.ctx
	}
	return s.ctx
}

// CancelTurn aborts the running turn, if any, and its sub-agents' turns:
// stopping a chat stops the work it delegated.
func (s *Session) CancelTurn() {
	s.mu.Lock()
	currentTurn := s.currentTurn
	s.mu.Unlock()
	if currentTurn != nil {
		currentTurn.cancel()
	}
	if s.params.Supervisor != nil {
		s.params.Supervisor.CancelDescendants(s)
	}
}

// AwaitingReview reports whether one of this session's own tool calls awaits
// the user's verdict — not a sub-agent's surfacing here; see tool.Agent.
func (s *Session) AwaitingReview() bool {
	for _, request := range s.reviews.Pending() {
		if request.Owner == s.id {
			return true
		}
	}
	return false
}

// TurnInFlight reports whether a turn is currently running, review pauses
// included — the window during which SendMessage queues an interjection
// instead of starting a new turn.
func (s *Session) TurnInFlight() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentTurn != nil
}

// QueuedMessages returns the interjections waiting for the next generation.
func (s *Session) QueuedMessages() []*aipb.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*aipb.Message(nil), s.queuedMessages...)
}

func (s *Session) hasQueuedMessages() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queuedMessages) > 0
}

// takeInputMessages drains the messages for the next generation request:
// pending messages (user text, tool results) first, then any queued
// interjections — tool results must directly answer the assistant message,
// so interjections always go last. Consumed interjections join the local
// mirror here, which is their true position in the conversation.
func (s *Session) takeInputMessages(t *turn) []*aipb.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	inputMessages := s.pendingInputMessages
	s.pendingInputMessages = nil
	if len(s.queuedMessages) > 0 {
		inputMessages = append(inputMessages, s.queuedMessages...)
		s.messages = append(s.messages, s.queuedMessages...)
		// This turn now answers them.
		t.waiters = append(t.waiters, s.takeWaitersLocked(s.queuedMessages)...)
		s.queuedMessages = nil
	}
	return inputMessages
}

// requeueInputMessages restores a failed generation's input for the next
// attempt. The server flags failed inputs with an error status — excluding
// them from provider history — so dropping them here would silently lose the
// user's words, and would leave tool calls orphaned (poisoning every later
// turn) whenever the failed input carried tool results.
func (s *Session) requeueInputMessages(inputMessages []*aipb.Message) {
	if len(inputMessages) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pendingInputMessages = append(inputMessages, s.pendingInputMessages...)
}

// saveChat persists local chat mutations from user actions (favorite, files).
// Ordering is already guaranteed — runTurn flushes before every generation
// and on turn exit — so mid-turn this is a no-op purely to avoid an RPC
// racing the open stream; the turn's own flush picks the mutation up.
func (s *Session) saveChat() error {
	if s.Busy() {
		return nil
	}
	return s.updateChat()
}

// updateChat is the single chat write: one masked update carrying every
// locally-owned field (title included). The server returns the full payload,
// adopted as the new local chat — no cloning, no local re-patching.
func (s *Session) updateChat() error {
	for {
		s.mu.Lock()
		chat, revision := s.chat, s.chatRevision
		s.mu.Unlock()

		// Not yet persisted (lazy creation): local mutations are carried by the
		// eventual CreateChat on the first send.
		if chat.GetName() == "" {
			return nil
		}
		updatedChat, err := s.store.UpdateChat(s.ctx, chat, "annotations", "labels", "title")
		if err != nil {
			return err
		}
		s.mu.Lock()
		edited := s.chatRevision != revision
		if !edited {
			s.chat = updatedChat
		}
		s.mu.Unlock()
		// An edit landed mid-write: adopting the response would revert it, and
		// it still has to be persisted.
		if !edited {
			return nil
		}
	}
}

// editChat applies a local edit to a copy of the chat.
func (s *Session) editChat(edit func(chat *aipb.Chat)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	chat := proto.CloneOf(s.chat)
	edit(chat)
	s.chat = chat
	s.chatRevision++
}

// flushChat is the end-of-turn chat write; errors surface as alerts.
func (s *Session) flushChat() {
	if err := s.updateChat(); err != nil {
		s.emitError(fmt.Errorf("saving chat: %w", err))
	}
}

// maybeGenerateTitle asynchronously titles an untitled chat. Only the user's
// own words are sent to the (cheap) summary model — context messages
// (system prompt, injected files) are excluded; generation is skipped
// entirely when no summary model is configured.
func (s *Session) maybeGenerateTitle() {
	s.mu.Lock()
	if s.titleGenerating || s.chat.GetName() == "" || s.chat.GetTitle() != "" {
		s.mu.Unlock()
		return
	}
	var parts []string
	for _, message := range s.messages {
		if message.GetRole() != aipb.Role_ROLE_USER || !isTypedByUser(message) {
			continue
		}
		for _, block := range ai.FilterBlocks(message.GetBlocks(), ai.BlockTypeText) {
			parts = append(parts, block.GetText())
		}
	}
	userText := strings.TrimSpace(strings.Join(parts, "\n"))
	if userText == "" {
		s.mu.Unlock()
		return
	}
	s.titleGenerating = true
	s.mu.Unlock()

	go func() {
		// Cap the excerpt: the summary model only needs the gist.
		const maxTitleInputLength = 2000
		if len(userText) > maxTitleInputLength {
			userText = userText[:maxTitleInputLength]
		}
		title, err := s.store.GenerateTitle(s.ctx, userText)

		s.mu.Lock()
		s.titleGenerating = false
		discard := err != nil || title == "" || s.chat.GetTitle() != ""
		s.mu.Unlock()
		if !discard {
			s.editChat(func(chat *aipb.Chat) { chat.Title = title })
		}
		if err != nil {
			s.emitError(fmt.Errorf("generating title: %w", err))
		}
		if discard {
			return
		}
		// Flush now if idle; if a turn is in flight, its end-of-turn flush
		// persists the title (the mask always includes it).
		if err := s.saveChat(); err != nil {
			s.emitError(fmt.Errorf("saving title: %w", err))
			return
		}
		s.refresh()
	}()
}

// ToggleFavorite flips the favorite tag on the chat and persists.
// Returns true if the chat is now a favorite.
func (s *Session) ToggleFavorite() bool {
	var favorite bool
	s.editChat(func(chat *aipb.Chat) {
		favorite = !store.IsFavorite(chat)
		store.SetFavoriteLabel(chat, favorite)
	})

	if err := s.saveChat(); err != nil {
		s.emitError(fmt.Errorf("saving favorite: %w", err))
	}
	return favorite
}

// Registry exposes the tool registry so the TUI can delegate tool-dictated
// request rendering (timeline.RequestRenderer).
func (s *Session) Registry() *tool.Registry {
	return s.registry
}
