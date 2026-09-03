package screen

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	aipb "github.com/malonaz/core/genproto/ai/v1"

	"github.com/malonaz/sgpt/cli/tui/editor"
	"github.com/malonaz/sgpt/cli/tui/keymap"
	"github.com/malonaz/sgpt/cli/tui/styles"
	"github.com/malonaz/sgpt/cli/tui/timeline"
	"github.com/malonaz/sgpt/cli/tui/widget"
	"github.com/malonaz/sgpt/internal/file"
	"github.com/malonaz/sgpt/internal/session"
)

type FocusedComponent int

const (
	FocusTextarea FocusedComponent = iota
	FocusViewport
)

type sessionEventMsg struct {
	event session.Event
}

var (
	chatKeyCycleFocus     = keymap.New("tab", "Toggle input/timeline focus")
	chatKeySubmit         = keymap.New("ctrl+j", "Send message / review tool call")
	chatKeyAccept         = keymap.New("alt+y", "Accept tool call under review")
	chatKeyAcceptAll      = keymap.New("alt+shift+y", "Accept all pending tool calls")
	chatKeyAlwaysAccept   = keymap.New("alt+shift+a", "Always accept this tool (session)")
	chatKeyReject         = keymap.New("alt+shift+r", "Reject tool call under review (input text = reason)")
	chatKeyCancel         = keymap.New("ctrl+c", "Cancel stream / close tab")
	chatKeyCycleReasoning = keymap.New("alt+t", "Cycle reasoning effort")
	chatKeyToggleFavorite = keymap.New("alt+shift+f", "Toggle favorite")
	chatKeyOpenAll        = keymap.New("alt+shift+o", "Open entire chat in $EDITOR")
	chatKeyPickTools      = keymap.New("alt+shift+t", "Select/unselect tools (fuzzy)")
	chatKeyPickFiles      = keymap.New("alt+shift+e", "Select/unselect files (fuzzy)")
	chatKeyDeleteMessage  = keymap.New("alt+d", "Delete selected message from the chat")
	chatKeyDeleteBelow    = keymap.New("alt+shift+d", "Delete selected message and everything below it")
	chatKeyEditMessage    = keymap.New("alt+e", "Edit selected user message and resend (truncates below)")
	chatKeyInfo           = keymap.New("alt+i", "Show chat info (context, tokens, cost)")
)

type ChatScreen struct {
	session *session.Session
	wrap    WrapFunc
	send    SendFunc

	titlebar *widget.TitleBar
	timeline *timeline.Model
	builder  *timeline.Builder
	input    *widget.Input
	spinner  spinner.Model

	lastInputHeight int

	// picker, when non-nil, is a modal fuzzy multi-select that captures all
	// keys; pickerApply consumes its selection on confirm.
	picker      *widget.Picker
	pickerApply func(selected []string) tea.Cmd

	// info, when non-nil, is the chat info modal: a read-only snapshot that
	// swallows every key (any key closes it).
	info *session.Info

	width            int
	height           int
	ready            bool
	focused          bool
	focusedComponent FocusedComponent
	// lastState detects the transition into review, to auto-jump exactly once.
	lastState session.State
}

func NewChatScreen(
	wrap WrapFunc,
	send SendFunc,
	chatSession *session.Session,
) *ChatScreen {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = styles.SpinnerStyle

	cs := &ChatScreen{
		session:          chatSession,
		wrap:             wrap,
		send:             send,
		titlebar:         widget.NewTitleBar(),
		timeline:         timeline.New(),
		builder:          timeline.NewBuilder(),
		input:            widget.NewInput(),
		spinner:          sp,
		focusedComponent: FocusTextarea,
	}
	cs.refreshTitle()
	cs.lastInputHeight = cs.input.Height()
	return cs
}

func (m *ChatScreen) Init() tea.Cmd {
	return tea.Batch(textarea.Blink, m.spinner.Tick, m.listenForSessionEvents(), m.repairHistory())
}

// repairHistory heals a history left with unanswered tool calls by a previous
// run (crash, kill, tab closed mid-review). Such a chat is otherwise dead on
// arrival: providers reject the whole conversation over a `tool_use` block
// with no matching `tool_result`, so every turn would fail before reaching the
// model. Runs once per screen, off the UI loop (it performs an RPC), and is a
// no-op for the overwhelmingly common healthy history.
func (m *ChatScreen) repairHistory() tea.Cmd {
	sess := m.session
	wrap := m.wrap
	return func() tea.Msg {
		if err := sess.RepairHistory(); err != nil {
			return wrap(AlertMsg{Text: fmt.Sprintf("Repairing interrupted tool calls failed: %v", err)})
		}
		return nil
	}
}

func (m *ChatScreen) Title() string {
	// Prefer the human-readable title (agent-provided or auto-generated).
	if title := m.session.Chat().GetTitle(); title != "" {
		return title
	}
	name := m.session.Chat().GetName()
	if name == "" {
		return "New Chat"
	}
	// Names are organizations/{org}/users/{user}/chats/{chat}; show the ID.
	return name[strings.LastIndex(name, "/")+1:]
}

func (m *ChatScreen) ShortTitle() string {
	return styles.Truncate(m.Title(), 20)
}

func (m *ChatScreen) Keymaps() []keymap.Map {
	return []keymap.Map{
		{Name: "Chat", Bindings: []keymap.Binding{
			chatKeySubmit, chatKeyAccept, chatKeyAcceptAll, chatKeyAlwaysAccept,
			chatKeyReject, chatKeyCancel, chatKeyCycleFocus,
			chatKeyCycleReasoning, chatKeyToggleFavorite,
			chatKeyOpenAll, chatKeyPickTools, chatKeyPickFiles,
			chatKeyDeleteMessage, chatKeyDeleteBelow, chatKeyEditMessage, chatKeyInfo,
		}},
		timeline.Keymap(),
		widget.InputKeymap(),
	}
}

func (m *ChatScreen) SetSize(width, height int) {
	m.width = width
	m.height = height
	if m.picker != nil {
		m.picker.SetSize(width, height)
	}
	m.recalculateLayout()
}

func (m *ChatScreen) OnFocus() tea.Cmd {
	m.focused = true
	if m.focusedComponent == FocusTextarea {
		return m.input.Focus()
	}
	return nil
}

func (m *ChatScreen) OnBlur() {
	m.focused = false
	m.input.Blur()
}

// IsStreaming reports whether the session is busy — drives the tab indicator
// and the quit guard.
func (m *ChatScreen) IsStreaming() bool {
	return m.session.Busy()
}

func (m *ChatScreen) Session() *session.Session {
	return m.session
}

func (m *ChatScreen) listenForSessionEvents() tea.Cmd {
	eventCh := m.session.Events()
	wrap := m.wrap
	return func() tea.Msg {
		event, ok := <-eventCh
		if !ok {
			return nil
		}
		return wrap(sessionEventMsg{event: event})
	}
}

func (m *ChatScreen) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.SetSize(msg.Width, msg.Height)
		return nil

	case sessionEventMsg:
		return tea.Batch(m.handleSessionEvent(msg.event), m.listenForSessionEvents())

	case editor.ClosedMsg:
		if m.focusedComponent == FocusTextarea {
			if msg.Modified {
				m.input.Textarea.SetValue(msg.Content)
				m.input.AdjustHeight()
			}
			return m.input.Focus()
		}
		return nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return cmd

	case tea.KeyPressMsg:
		// The info modal is read-only: any key dismisses it, and none reach
		// the chat underneath.
		if m.info != nil {
			m.info = nil
			return nil
		}
		if m.picker != nil {
			return m.handlePickerKey(msg)
		}
		return m.handleKeyPress(msg)
	}

	if m.focusedComponent == FocusTextarea {
		return m.updateInput(msg)
	}
	return nil
}

func (m *ChatScreen) handleSessionEvent(event session.Event) tea.Cmd {
	// Drain on every event: error notifications share the lossy channel, so
	// the queue — not the notification — is the source of truth.
	var cmds []tea.Cmd
	for _, err := range m.session.Errors() {
		cmds = append(cmds, m.alert(err.Error()))
	}
	if _, ok := event.(session.RefreshEvent); ok {
		m.refresh()
		m.maybeJumpToReview()
	}
	return tea.Batch(cmds...)
}

// alert surfaces text as an app-level alert.
func (m *ChatScreen) alert(text string) tea.Cmd {
	wrap := m.wrap
	return func() tea.Msg { return wrap(AlertMsg{Text: text}) }
}

func (m *ChatScreen) handleKeyPress(msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case key.Matches(msg, chatKeyCycleFocus.Key):
		return m.cycleFocus()
	case key.Matches(msg, chatKeyAccept.Key):
		return m.withReviewTarget(func(toolCallID string) tea.Cmd {
			m.session.ApproveToolCall(toolCallID)
			return nil
		})
	case key.Matches(msg, chatKeyAcceptAll.Key):
		m.session.ApproveAllToolCalls()
		return nil
	case key.Matches(msg, chatKeyAlwaysAccept.Key):
		return m.withReviewTarget(func(toolCallID string) tea.Cmd {
			m.session.AlwaysApproveTool(m.toolNameForCall(toolCallID))
			return nil
		})
	case key.Matches(msg, chatKeyReject.Key):
		return m.withReviewTarget(func(toolCallID string) tea.Cmd {
			reason := m.input.Value()
			m.input.Reset()
			m.session.RejectToolCall(toolCallID, reason)
			return nil
		})
	case key.Matches(msg, chatKeyCycleReasoning.Key):
		m.cycleReasoningEffort()
		return nil
	case key.Matches(msg, chatKeyToggleFavorite.Key):
		return m.toggleFavorite()
	case key.Matches(msg, chatKeyOpenAll.Key):
		return editor.Open(timeline.ConversationText(m.session.Messages()), "md")
	case key.Matches(msg, chatKeyPickTools.Key):
		return m.openToolPicker()
	case key.Matches(msg, chatKeyPickFiles.Key):
		return m.openFilePicker()
	case key.Matches(msg, chatKeyDeleteMessage.Key):
		return m.deleteSelectedMessage()
	case key.Matches(msg, chatKeyDeleteBelow.Key):
		return m.deleteMessagesBelowSelected()
	case key.Matches(msg, chatKeyEditMessage.Key):
		return m.editSelectedMessage()
	case key.Matches(msg, chatKeyInfo.Key):
		// Snapshotted on open: the modal is a still frame, so a streaming
		// turn never mutates the numbers under the reader's eyes.
		m.info = m.session.Info()
		return nil
	}

	switch msg.String() {
	case "ctrl+c":
		// TurnInFlight covers the whole turn — pre-stream window, streaming,
		// tool execution and review pauses; CancelTurn aborts it wherever it
		// is and the turn resolves cleanly (cancelled tool results, inputs
		// re-queued).
		if m.session.TurnInFlight() {
			m.session.CancelTurn()
			return nil
		}
		return func() tea.Msg { return CloseTabMsg{} }
	case "ctrl+j":
		return m.submit()
	}

	// Timeline stays navigable at all times — including during tool review.
	if m.focusedComponent == FocusViewport {
		return m.timeline.HandleKey(msg, m.alert)
	}

	if cmd := m.input.HandleKey(msg); cmd != nil {
		return cmd
	}
	return m.updateInput(msg)
}

func (m *ChatScreen) submit() tea.Cmd {
	// An empty ctrl+j while a call awaits review approves it. Composed text
	// is always a message — an interjection queued for the next generation
	// when a turn is in flight — never an implicit verdict. Rejection stays
	// explicit (alt+shift+r).
	pendingToolCallID := m.reviewTarget()
	if pendingToolCallID != "" && m.input.Value() == "" {
		m.session.ApproveToolCall(pendingToolCallID)
		return nil
	}

	text := m.input.Submit()
	if text == "" {
		return nil
	}
	m.timeline.ClearSelection()
	sess := m.session
	m.refresh()
	m.timeline.GotoBottom()
	var cmds []tea.Cmd
	if pendingToolCallID != "" {
		cmds = append(cmds, m.alert("Message queued — the tool call still awaits your verdict"))
	}
	cmds = append(cmds, m.spinner.Tick, func() tea.Msg {
		// Queues an interjection when a turn is in flight; starts a turn
		// otherwise. Refreshes arrive on the session event channel; emitting
		// one here too would double-invoke maybeJumpToReview and can consume
		// the idle→review edge it keys off.
		sess.SendMessage(text)
		return nil
	})
	return tea.Batch(cmds...)
}

// reviewTarget returns the tool call a verdict applies to: the selected one
// when navigating the timeline, otherwise any pending call. Empty when nothing
// is awaiting review.
func (m *ChatScreen) reviewTarget() string {
	pending := m.session.PendingToolCallIDs()
	if len(pending) == 0 {
		return ""
	}
	if m.focusedComponent == FocusViewport {
		if item, ok := m.timeline.SelectedItem().(*timeline.ToolCallItem); ok {
			if toolCallID := item.ToolCall.GetId(); pending[toolCallID] {
				return toolCallID
			}
		}
	}
	// Map iteration is unordered, but reviews are awaited one at a time, so
	// there is normally exactly one pending call.
	for toolCallID := range pending {
		return toolCallID
	}
	return ""
}

// withReviewTarget applies a verdict to the current review target, if any.
func (m *ChatScreen) withReviewTarget(verdict func(toolCallID string) tea.Cmd) tea.Cmd {
	toolCallID := m.reviewTarget()
	if toolCallID == "" {
		return nil
	}
	return verdict(toolCallID)
}

// toolNameForCall resolves a tool call ID to its tool name via the timeline.
func (m *ChatScreen) toolNameForCall(toolCallID string) string {
	for _, item := range m.buildItems() {
		if toolCallItem, ok := item.(*timeline.ToolCallItem); ok &&
			toolCallItem.ToolCall.GetId() == toolCallID {
			return toolCallItem.ToolCall.GetName()
		}
	}
	return ""
}

// deleteSelectedMessage soft-deletes the message under the timeline cursor,
// dropping it from the conversation sent to the model. The escape hatch for a
// message that breaks generation — an oversized paste, a poisoned tool result,
// or the tool call whose missing result makes the provider reject the chat.
func (m *ChatScreen) deleteSelectedMessage() tea.Cmd {
	messageName, alert := m.deletableSelection("alt+d")
	if alert != nil {
		return alert
	}

	sess := m.session
	wrap := m.wrap
	// Selection points at an item that is about to vanish; clear it so the
	// cursor doesn't dangle on a stale ID after the rebuild.
	m.timeline.ClearSelection()
	return func() tea.Msg {
		if err := sess.DeleteMessage(messageName); err != nil {
			return wrap(AlertMsg{Text: fmt.Sprintf("Delete failed: %v", err)})
		}
		return wrap(AlertMsg{Text: "Message deleted"})
	}
}

// deleteMessagesBelowSelected truncates the conversation from the message
// under the cursor (inclusive), so the chat can be resumed from just above it.
func (m *ChatScreen) deleteMessagesBelowSelected() tea.Cmd {
	messageName, alert := m.deletableSelection("alt+shift+d")
	if alert != nil {
		return alert
	}

	sess := m.session
	wrap := m.wrap
	// The anchor vanishes too; clear so the cursor doesn't dangle on it.
	m.timeline.ClearSelection()
	return func() tea.Msg {
		count, err := sess.DeleteMessagesFrom(messageName)
		if err != nil {
			return wrap(AlertMsg{Text: fmt.Sprintf("Delete failed: %v", err)})
		}
		return wrap(AlertMsg{Text: fmt.Sprintf("%d messages deleted", count)})
	}
}

// editSelectedMessage rewinds the chat to just above the selected user message
// and loads its text into the input, so a bad prompt is fixed in one key.
// ctrl+j resends as usual; nothing is sent implicitly.
func (m *ChatScreen) editSelectedMessage() tea.Cmd {
	messageName, alert := m.deletableSelection("alt+e")
	if alert != nil {
		return alert
	}
	text := m.session.UserMessageText(messageName)
	if text == "" {
		return m.alert("Only user messages can be edited")
	}
	// Never clobber a draft silently.
	if m.input.Value() != "" {
		return m.alert("Input has unsent text — clear it first")
	}

	// Text lands in the input immediately; the truncation runs off the UI
	// loop, and a failure still leaves the user holding their text.
	m.timeline.ClearSelection()
	m.input.SetValue(text)
	focus := m.cycleFocus()
	sess := m.session
	wrap := m.wrap
	return tea.Batch(focus, func() tea.Msg {
		if _, err := sess.DeleteMessagesFrom(messageName); err != nil {
			return wrap(AlertMsg{Text: fmt.Sprintf("Edit failed: %v", err)})
		}
		return nil
	})
}

// deletableSelection resolves the message a delete key acts on, or an alert
// explaining why nothing can be deleted right now.
func (m *ChatScreen) deletableSelection(keyHint string) (string, tea.Cmd) {
	if m.focusedComponent != FocusViewport {
		return "", m.alert(fmt.Sprintf("Deleting a message needs timeline focus — tab to navigate, then %s", keyHint))
	}
	// A turn in flight is still appending to (and persisting) the history;
	// deleting underneath it would race those writes. TurnInFlight, not Busy:
	// a review pause is still mid-turn.
	if m.session.TurnInFlight() {
		return "", m.alert("Cannot delete while the turn is running — ctrl+c to cancel first")
	}
	messageName := m.timeline.SelectedMessageName()
	if messageName == "" {
		return "", m.alert("No deletable message selected")
	}
	return messageName, nil
}

// maybeJumpToReview focuses a pending tool call when a turn pauses for review,
// so the user lands directly on what needs their verdict.
func (m *ChatScreen) maybeJumpToReview() {
	state := m.session.State()
	entered := state == session.StateAwaitingReview && m.lastState != session.StateAwaitingReview
	m.lastState = state
	if !entered {
		return
	}
	// Don't steal focus mid-composition: blurring the input silently drops
	// keystrokes into the timeline while the user is typing a message.
	if m.input.Value() != "" {
		return
	}
	if toolCallID := m.reviewTarget(); toolCallID != "" {
		m.focusToolCall(toolCallID)
	}
}

// focusToolCall moves timeline focus onto the given call's item.
func (m *ChatScreen) focusToolCall(toolCallID string) {
	m.focusedComponent = FocusViewport
	m.input.Blur()
	m.timeline.SetFocused(true)
	m.timeline.SelectFunc(func(item timeline.Item) bool {
		toolCallItem, ok := item.(*timeline.ToolCallItem)
		return ok && toolCallItem.ToolCall.GetId() == toolCallID
	})
}

func (m *ChatScreen) toggleFavorite() tea.Cmd {
	sess := m.session
	wrap := m.wrap
	return func() tea.Msg {
		isFavorite := sess.ToggleFavorite()
		label := "added to"
		if !isFavorite {
			label = "removed from"
		}
		return wrap(AlertMsg{Text: fmt.Sprintf("Chat %s favorites", label)})
	}
}

func (m *ChatScreen) handlePickerKey(msg tea.KeyPressMsg) tea.Cmd {
	done, canceled := m.picker.HandleKey(msg)
	if !done {
		return nil
	}
	picker, apply := m.picker, m.pickerApply
	m.picker, m.pickerApply = nil, nil
	if canceled {
		return nil
	}
	cmd := apply(picker.Selected())
	m.refresh()
	return cmd
}

// openToolPicker lists every selectable tool (all builtins + configured
// engines), regardless of the --tool flags; the selection takes effect on
// the next request.
func (m *ChatScreen) openToolPicker() tea.Cmd {
	names := m.session.AvailableToolNames()
	if len(names) == 0 {
		return m.alert("No tools configured")
	}
	enabledToolNameSet := m.session.EnabledTools()
	items := make([]widget.PickerItem, 0, len(names))
	for _, name := range names {
		items = append(items, widget.PickerItem{Label: name, Selected: enabledToolNameSet[name]})
	}
	m.picker = widget.NewPicker("🔧 Tools", items)
	m.picker.SetSize(m.width, m.height)
	sess := m.session
	wrap := m.wrap
	m.pickerApply = func(selected []string) tea.Cmd {
		return func() tea.Msg {
			// May dial a tool engine on first enablement — off the UI loop.
			if err := sess.SetEnabledTools(selected); err != nil {
				return wrap(AlertMsg{Text: fmt.Sprintf("Enabling tools failed: %v", err)})
			}
			m.refreshTitle()
			return wrap(AlertMsg{Text: fmt.Sprintf("Tools enabled: %d", len(selected))})
		}
	}
	return nil
}

// openFilePicker lists the injected files (selected) plus files discovered
// under the cwd (unselected), so files can be added as well as removed.
func (m *ChatScreen) openFilePicker() tea.Cmd {
	injected := m.session.InjectedFiles()
	injectedPathSet := make(map[string]bool, len(injected))
	items := make([]widget.PickerItem, 0, len(injected))
	for _, path := range injected {
		injectedPathSet[path] = true
		items = append(items, widget.PickerItem{Label: path, Selected: true})
	}
	for _, path := range file.Discover(".", 2000) {
		if !injectedPathSet[path] {
			items = append(items, widget.PickerItem{Label: path})
		}
	}
	m.picker = widget.NewPicker("📎 Files", items)
	m.picker.SetSize(m.width, m.height)
	m.pickerApply = func(selected []string) tea.Cmd {
		sess := m.session
		// SetInjectedFiles performs RPCs (soft-deleting removed file
		// messages) — run it off the UI loop.
		return func() tea.Msg {
			sess.SetInjectedFiles(selected)
			return nil
		}
	}
	return nil
}

func (m *ChatScreen) cycleFocus() tea.Cmd {
	switch m.focusedComponent {
	case FocusTextarea:
		m.focusedComponent = FocusViewport
		m.input.Blur()
		m.timeline.SetFocused(true)
		if m.timeline.SelectedItem() == nil {
			m.timeline.SelectLast()
		}
		return nil
	default:
		m.focusedComponent = FocusTextarea
		m.timeline.SetFocused(false)
		return m.input.Focus()
	}
}

func (m *ChatScreen) cycleReasoningEffort() {
	params := m.session.Params()
	switch params.ReasoningEffort {
	case aipb.ReasoningEffort_REASONING_EFFORT_UNSPECIFIED:
		m.session.SetReasoningEffort(aipb.ReasoningEffort_REASONING_EFFORT_LOW)
	case aipb.ReasoningEffort_REASONING_EFFORT_LOW:
		m.session.SetReasoningEffort(aipb.ReasoningEffort_REASONING_EFFORT_MEDIUM)
	case aipb.ReasoningEffort_REASONING_EFFORT_MEDIUM:
		m.session.SetReasoningEffort(aipb.ReasoningEffort_REASONING_EFFORT_HIGH)
	case aipb.ReasoningEffort_REASONING_EFFORT_HIGH:
		m.session.SetReasoningEffort(aipb.ReasoningEffort_REASONING_EFFORT_UNSPECIFIED)
	}
	m.refreshTitle()
}

func (m *ChatScreen) updateInput(msg tea.Msg) tea.Cmd {
	cmd := m.input.Update(msg)
	if m.input.Height() != m.lastInputHeight {
		m.lastInputHeight = m.input.Height()
		m.recalculateLayout()
	}
	return cmd
}

func (m *ChatScreen) buildItems() []timeline.Item {
	// Injected files render as regular timeline entries (label-detected by
	// the builder), so there is no dedicated aggregate item here.
	items := m.builder.Build(
		m.session.Messages(),
		m.session.StreamingMessage(),
		m.session.ExecutingToolCallID(),
		m.session.PendingToolCallIDs(),
		m.session.Registry(),
	)
	if err := m.session.StreamError(); err != nil {
		if session.IsCancelError(err) {
			items = append(items, timeline.NewCancelledItem("turn-cancelled"))
		} else {
			items = append(items, timeline.NewErrorItem("stream-error", err.Error()))
		}
	}
	// Interjections render at the bottom until the turn consumes them into
	// the history proper.
	for i, queuedMessage := range m.session.QueuedMessages() {
		items = append(items, timeline.NewQueuedItem(fmt.Sprintf("queued-%d", i), messageText(queuedMessage)))
	}
	return items
}

// messageText joins a message's text blocks for single-line display.
func messageText(message *aipb.Message) string {
	var parts []string
	for _, block := range message.GetBlocks() {
		if block.GetText() != "" {
			parts = append(parts, block.GetText())
		}
	}
	return strings.Join(parts, " ")
}

func (m *ChatScreen) refresh() {
	wasAtBottom := m.timeline.AtBottom()
	m.timeline.SetItems(m.buildItems())
	m.refreshTitle()
	m.refreshPlaceholder()
	m.recalculateLayout()
	if wasAtBottom {
		m.timeline.GotoBottom()
	}
}

func (m *ChatScreen) refreshPlaceholder() {
	if toolCallID := m.reviewTarget(); toolCallID != "" {
		m.input.Textarea.Placeholder = fmt.Sprintf(
			"reviewing %s — ctrl+j: accept, alt+shift+r: reject (input = reason), alt+shift+a: always accept",
			m.toolNameForCall(toolCallID))
		return
	}
	m.input.Textarea.Placeholder = "Type your message... (ctrl+j: send, tab: navigate, alt+h: help)"
}

func (m *ChatScreen) refreshTitle() {
	m.titlebar.Refresh(m.session.Params(), m.session.TotalModelUsage(), m.session.LastModelUsage(), m.session.Price())
}

func (m *ChatScreen) recalculateLayout() {
	if m.width == 0 || m.height == 0 {
		return
	}

	m.titlebar.SetWidth(m.width)

	// The input stays visible while busy — interjections are typed mid-turn —
	// with a one-line status (spinner) above it.
	bottomHeight := m.input.Height()
	if m.session.Busy() {
		bottomHeight++
	}
	viewportHeight := m.height - m.titlebar.Height() - bottomHeight - 1
	if viewportHeight < styles.MinViewportHeight {
		viewportHeight = styles.MinViewportHeight
	}

	m.timeline.SetSize(m.width, viewportHeight)
	m.input.SetWidth(m.width)

	if !m.ready {
		m.ready = true
		m.refresh()
		m.timeline.GotoBottom()
	}
}

func (m *ChatScreen) busyLabel() string {
	if m.session.State() == session.StateExecutingTools {
		return "executing tool... (ctrl+j: queue a message · ctrl+c: cancel)"
	}
	return "thinking... (ctrl+j: queue a message · ctrl+c: cancel)"
}

func (m *ChatScreen) View() string {
	if !m.ready {
		return "Initializing..."
	}
	if m.info != nil {
		return widget.RenderInfo(m.info, m.width, m.height)
	}
	if m.picker != nil {
		return m.picker.View()
	}

	var b strings.Builder
	b.WriteString(m.titlebar.View())
	b.WriteString("\n")
	b.WriteString(m.timeline.View())
	b.WriteString("\n")
	if m.session.Busy() {
		b.WriteString(m.spinner.View())
		b.WriteString(" ")
		b.WriteString(styles.DimTextStyle.Render(m.busyLabel()))
		b.WriteString("\n")
	}
	b.WriteString(m.input.View())
	return b.String()
}

var _ Screen = (*ChatScreen)(nil)
