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
	"sort"
	"strings"
	"sync"

	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/file"
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

// verdict is the user's answer to a tool call review.
type verdict struct {
	approved bool
	reason   string
}

// pendingReview is a tool call awaiting a verdict: the tool's name (so the
// user can whitelist it) and the channel its turn goroutine is blocked on.
type pendingReview struct {
	toolName  string
	verdictCh chan verdict
}

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
}

// Session drives a single chat conversation.
type Session struct {
	ctx      context.Context
	params   Params
	store    *store.Store
	registry *tool.Registry

	mu   sync.Mutex
	chat *aipb.Chat
	// messages is the local mirror of the chat's server-side message
	// history, oldest first. Input messages appended locally may lack a
	// resource name (the server persists them without echoing them back).
	messages          []*aipb.Message
	streamingMessage  *aipb.Message
	streamError       error
	state             State
	executingToolCall string
	// turnCtx scopes one full turn (context RPCs, streams, tool loops); it
	// exists exactly while a turn runs and cancelTurn aborts it wherever it
	// is — including before the first stream opens.
	turnCtx    context.Context
	cancelTurn context.CancelFunc
	// titleGenerating guards against launching concurrent title generations.
	titleGenerating bool

	// autoAcceptedToolNameSet holds tools the user marked "always accept";
	// their calls skip manual review for the rest of the session.
	autoAcceptedToolNameSet map[string]bool

	// pendingReviews holds one entry per tool call currently awaiting user
	// review. The turn goroutine blocks on the channel; the UI answers by
	// tool call ID. This *is* the review state — there is nothing else to
	// track, since a reviewed call executes immediately and its result is
	// terminal.
	pendingReviews map[string]pendingReview

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

	// enabledUserToolNameSet is the user-facing tool selection;
	// enabledAdvertisedNameSet is its expansion to the tool/tool-set names
	// actually advertised to the model.
	enabledUserToolNameSet   map[string]bool
	enabledAdvertisedNameSet map[string]bool

	totalModelUsage *aipb.ModelUsage
	lastModelUsage  *aipb.ModelUsage

	// pendingErrors queues non-fatal errors for the TUI. Errors are never
	// dropped, so they are held here rather than in the lossy event channel.
	pendingErrors []error

	// price memoizes the sum of message prices; every render reads it.
	price      float64
	priceValid bool

	// onTurnComplete fires when a turn reaches a terminal state: final answer
	// (no tool calls), stream error, or tool-processing failure. It does NOT
	// fire while awaiting review — the sub-agent launcher uses it to collect
	// the final answer after the user resolves everything in the tab.
	onTurnComplete func(finalText string, err error)

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
	s := &Session{
		ctx:                           ctx,
		params:                        params,
		store:                         chatStore,
		registry:                      registry,
		chat:                          chat,
		messages:                      messages,
		autoAcceptedToolNameSet:       map[string]bool{},
		pendingReviews:                map[string]pendingReview{},
		injectedFilePathToMessageName: map[string]string{},
		totalModelUsage:               &aipb.ModelUsage{},
		lastModelUsage:                &aipb.ModelUsage{},
		eventCh:                       make(chan Event, 64),
	}
	// Tools execute against this session's history (the registry is one
	// instance shared across main chat and sub-agents): stamped on the
	// context so tools can derive what the model has already seen.
	s.ctx = tool.WithHistory(s.ctx, s.historyForTools)
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
	s.invalidatePrice()
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
	s.emit(RefreshEvent{})
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

func (s *Session) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
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

func (s *Session) setState(state State) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = state
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

func (s *Session) TotalModelUsage() *aipb.ModelUsage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.totalModelUsage
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

// Price sums the server-priced messages of the chat: each message carries its
// own authoritative cost, so no client-side pricing math is needed.
func (s *Session) Price() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.priceValid {
		return s.price
	}
	// Recomputed in full: messages are not strictly append-only (optimistic
	// placeholders get swapped, injected files removed), so an incremental
	// tally would drift. Memoized because every render asks for this.
	s.price = 0
	for _, message := range s.messages {
		s.price += message.GetPrice()
	}
	s.priceValid = true
	return s.price
}

// invalidatePrice must be called whenever s.messages changes. Caller holds
// the lock.
func (s *Session) invalidatePrice() {
	s.priceValid = false
}

func (s *Session) SetReasoningEffort(effort aipb.ReasoningEffort) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.params.ReasoningEffort = effort
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
			s.invalidatePrice()
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
		s.invalidatePrice()
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
				s.invalidatePrice()
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

	for _, name := range messageNamesToDelete {
		if err := s.store.DeleteMessage(s.ctx, name); err != nil {
			return fmt.Errorf("deleting message: %w", err)
		}
	}

	deletedNameSet := make(map[string]bool, len(messageNamesToDelete))
	for _, name := range messageNamesToDelete {
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
	s.invalidatePrice()
	s.mu.Unlock()
	s.refresh()
	return nil
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

// ensureContext persists the one-time context messages (system prompt, newly
// injected files) before a generation. Idempotent.
func (s *Session) ensureContext() error {
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
		s.invalidatePrice()
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
		s.invalidatePrice()
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

// IsToolAutoAccepted reports whether the user whitelisted this tool.
func (s *Session) IsToolAutoAccepted(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.autoAcceptedToolNameSet[name]
}

func (s *Session) SetOnTurnComplete(callback func(finalText string, err error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onTurnComplete = callback
}

// notifyTurnComplete invokes the callback outside the lock: it may block
// (delivering the sub-agent result) and must not deadlock the session.
func (s *Session) notifyTurnComplete(finalText string, err error) {
	s.mu.Lock()
	callback := s.onTurnComplete
	s.mu.Unlock()
	if callback != nil {
		callback(finalText, err)
	}
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

func (s *Session) SendMessage(text string) {
	s.beginTurn()
	defer s.endTurn()
	s.setState(StateStreaming)
	// Optimistic render: the user's message must appear immediately. The
	// context RPCs below (system prompt, injected files) are a network round
	// trip that would otherwise leave the timeline empty.
	userMessage := ai.NewUserMessage(ai.NewTextBlock(text))
	s.mu.Lock()
	s.streamError = nil
	s.messages = append(s.messages, userMessage)
	s.pendingInputMessages = append(s.pendingInputMessages, userMessage)
	s.invalidatePrice()
	s.mu.Unlock()
	s.refresh()

	if err := s.ensureContext(); err != nil {
		s.setState(StateIdle)
		// The turn never ran: drop the optimistic message from the queue so a
		// retry doesn't send it twice.
		s.rollbackPendingInput(userMessage)
		s.emitError(err)
		s.notifyTurnComplete("", err)
		return
	}

	s.refresh()
	s.runTurn()
}

// rollbackPendingInput removes an optimistically appended message after the
// turn failed before reaching the model.
func (s *Session) rollbackPendingInput(message *aipb.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, pendingMessage := range s.pendingInputMessages {
		if pendingMessage == message {
			s.pendingInputMessages = append(s.pendingInputMessages[:i], s.pendingInputMessages[i+1:]...)
			break
		}
	}
}

// beginTurn arms the turn-scoped context; from here on CancelTurn aborts any
// turn work, network round trips included.
func (s *Session) beginTurn() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.turnCtx, s.cancelTurn = context.WithCancel(s.ctx)
}

// endTurn releases the turn context; outside a turn there is nothing to cancel.
func (s *Session) endTurn() {
	s.mu.Lock()
	cancelTurn := s.cancelTurn
	s.turnCtx, s.cancelTurn = nil, nil
	s.mu.Unlock()
	if cancelTurn != nil {
		cancelTurn()
	}
}

// turnContext returns the running turn's context, falling back to the
// session context outside a turn.
func (s *Session) turnContext() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.turnCtx != nil {
		return s.turnCtx
	}
	return s.ctx
}

// CancelTurn aborts the running turn, if any.
func (s *Session) CancelTurn() {
	s.mu.Lock()
	cancelTurn := s.cancelTurn
	s.mu.Unlock()
	if cancelTurn != nil {
		cancelTurn()
	}
}

// takePendingInputMessages drains the queue of new messages for the next
// generation request.
func (s *Session) takePendingInputMessages() []*aipb.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	inputMessages := s.pendingInputMessages
	s.pendingInputMessages = nil
	return inputMessages
}

// runTurn executes a complete turn: stream → process tool calls → save.
// Auto-execute tool calls run immediately (some already ran eagerly during
// streaming). Manual ones pause the turn for user review.
func (s *Session) runTurn() {
	// A turn never leaves unpersisted chat state behind, and a generation
	// never starts on top of it: every loop iteration flushes before
	// streaming, and the turn flushes once more on exit.
	defer s.flushChat()
	for {
		s.flushChat()
		generatedMessage, err := s.stream(s.takePendingInputMessages())

		s.mu.Lock()
		ai.AggregateModelUsage(s.totalModelUsage, s.lastModelUsage)
		*s.lastModelUsage = aipb.ModelUsage{}
		s.mu.Unlock()

		if err != nil {
			s.refresh()
			s.notifyTurnComplete("", err)
			return
		}

		var toolCalls []*aipb.ToolCall
		for _, block := range ai.FilterBlocks(generatedMessage.GetBlocks(), ai.BlockTypeToolCall) {
			toolCalls = append(toolCalls, block.GetToolCall())
		}

		if len(toolCalls) == 0 {
			s.maybeGenerateTitle()
			s.refresh()
			s.notifyTurnComplete(s.lastAssistantText(), nil)
			return
		}

		// Blocks on user review where required; every call ends up with a
		// terminal result. Loop: the top-of-loop flush persists the tool side
		// effects before the next generation.
		s.executeToolCalls(generatedMessage, toolCalls)
		s.setState(StateStreaming)
	}
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
	s.mu.Lock()
	chat := s.chat
	s.mu.Unlock()

	updatedChat, err := s.store.UpdateChat(s.ctx, chat, "annotations", "labels", "title")
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.chat = updatedChat
	s.mu.Unlock()
	return nil
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
		if message.GetRole() != aipb.Role_ROLE_USER || store.IsContextMessage(message) {
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
		if !discard {
			s.chat.Title = title
		}
		s.mu.Unlock()
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
	s.mu.Lock()
	favorite := !store.IsFavorite(s.chat)
	store.SetFavoriteLabel(s.chat, favorite)
	s.mu.Unlock()

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
