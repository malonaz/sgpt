package screen

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	aipb "github.com/malonaz/core/genproto/ai/v1"

	"github.com/malonaz/sgpt/cli/tui/editor"
	"github.com/malonaz/sgpt/cli/tui/format"
	"github.com/malonaz/sgpt/cli/tui/keymap"
	"github.com/malonaz/sgpt/cli/tui/styles"
	"github.com/malonaz/sgpt/cli/tui/timeline"
	"github.com/malonaz/sgpt/cli/tui/widget"
	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/file"
	"github.com/malonaz/sgpt/internal/history"
	"github.com/malonaz/sgpt/internal/review"
	"github.com/malonaz/sgpt/internal/session"
	"github.com/malonaz/sgpt/internal/tool"
	"github.com/malonaz/sgpt/internal/tool/agent"
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
	chatKeyCycleFocus     = keymap.New("tab", "Switch input and timeline")
	chatKeySubmit         = keymap.New("ctrl+j", "Send, or accept the review")
	chatKeyAccept         = keymap.New("alt+y", "Accept the tool call")
	chatKeyAcceptAll      = keymap.New("alt+shift+y", "Accept all tool calls")
	chatKeyAlwaysAccept   = keymap.New("alt+shift+a", "Always accept this tool")
	chatKeyReject         = keymap.New("alt+shift+r", "Reject (input is the reason)")
	chatKeyCancel         = keymap.New("ctrl+c", "Cancel turn, or clear input")
	chatKeyBack           = keymap.New("esc", "Back to the input")
	chatKeyPageUp         = keymap.New("pgup", "Page up")
	chatKeyPageDown       = keymap.New("pgdown", "Page down")
	chatKeyCycleReasoning = keymap.New("alt+t", "Cycle reasoning effort")
	chatKeyToggleFavorite = keymap.New("alt+shift+f", "Toggle favorite")
	chatKeyOpenAll        = keymap.New("alt+shift+o", "Open chat in $EDITOR")
	chatKeyPickTools      = keymap.New("alt+shift+t", "Pick tools")
	chatKeyPickFiles      = keymap.New("alt+shift+e", "Pick files")
	chatKeyDeleteMessage  = keymap.New("alt+d", "Delete message")
	chatKeyDeleteBelow    = keymap.New("alt+shift+d", "Delete message and below")
	chatKeyEditMessage    = keymap.New("alt+e", "Edit and resend message")
	chatKeyInfo           = keymap.New("alt+i", "Chat info")
	chatKeyFocusLauncher  = keymap.New("alt+u", "Go to launcher")
	chatKeyNextAgent      = keymap.New("alt+g", "Go to next sub-agent")
)

const (
	// minTimelineHeight is what the input leaves the timeline as it grows.
	minTimelineHeight = 4
)

var (
	reviewStateStyle  = lipgloss.NewStyle().Foreground(styles.ColorWarning).Bold(true)
	spinnerStyle      = lipgloss.NewStyle().Foreground(styles.ColorBrand)
	contextWarnStyle  = lipgloss.NewStyle().Foreground(styles.ColorWarning)
	contextAlertStyle = lipgloss.NewStyle().Foreground(styles.ColorDanger)
)

// ChatEnv is what the app shares with every chat screen.
type ChatEnv struct {
	Agents AgentDirectory
	// History is the input history every tab shares.
	History *history.Store
	// DefaultRole is the configured role selector; a chat on any other role
	// names it in the status bar. Empty when unknown.
	DefaultRole string
}

type ChatScreen struct {
	session *session.Session
	env     *ChatEnv
	agents  AgentDirectory
	wrap    WrapFunc
	send    SendFunc

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
	// document, when non-nil, is a tool call's full request in a scrollable
	// modal that captures all keys.
	document *widget.Document

	width            int
	height           int
	ready            bool
	focused          bool
	focusedComponent FocusedComponent
	// seenReviews are the pending tool calls already announced: each new one
	// jumps or alerts exactly once.
	seenReviews map[string]bool
	// agentCursor is the sub-agent alt+g last went to.
	agentCursor int
	// turnInFlight is the last observed TurnInFlight, to catch turns
	// starting and ending.
	turnInFlight bool
	turnStart    time.Time
	// cancelling is set by ctrl+c until the cancelled turn winds down, so
	// a press during the wind-down is visible and a further one moves on.
	cancelling bool
	// activeTool names the call running or under review, resolved on
	// refresh rather than per frame; activeTitled marks it as the model's
	// title, already a verb phrase.
	activeTool   string
	activeTitled bool
	// unread is the outcome of a turn that ended while the tab was unfocused.
	unread widget.TabState
	// Decided at layout time, so View never renders a line the layout did
	// not reserve.
	showAgents bool
}

func NewChatScreen(
	wrap WrapFunc,
	send SendFunc,
	chatSession *session.Session,
	env *ChatEnv,
) *ChatScreen {
	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot), spinner.WithStyle(spinnerStyle))

	cs := &ChatScreen{
		session:          chatSession,
		env:              env,
		agents:           env.Agents,
		wrap:             wrap,
		send:             send,
		timeline:         timeline.New(),
		builder:          timeline.NewBuilder(),
		input:            widget.NewInput(env.History),
		spinner:          sp,
		focusedComponent: FocusTextarea,
		agentCursor:      -1,
	}
	cs.refreshPlaceholder()
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
			return wrap(AlertMsg{Text: fmt.Sprintf("Repairing interrupted tool calls failed: %v", err), Level: AlertError})
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

func (m *ChatScreen) Keymaps() []keymap.Map {
	return []keymap.Map{
		{Name: "Chat", Bindings: []keymap.Binding{
			chatKeySubmit, chatKeyAccept, chatKeyAcceptAll, chatKeyAlwaysAccept,
			chatKeyReject, chatKeyCancel, chatKeyCycleFocus, chatKeyBack,
			chatKeyPageUp, chatKeyPageDown,
			chatKeyCycleReasoning, chatKeyToggleFavorite,
			chatKeyOpenAll, chatKeyPickTools, chatKeyPickFiles,
			chatKeyDeleteMessage, chatKeyDeleteBelow, chatKeyEditMessage, chatKeyInfo,
			chatKeyFocusLauncher, chatKeyNextAgent,
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
	if m.document != nil {
		m.document.SetSize(width, height)
	}
	m.recalculateLayout()
}

func (m *ChatScreen) OnFocus() tea.Cmd {
	m.focused = true
	m.unread = widget.TabIdle
	// Ticks reach only the active tab, so a background spinner's chain died.
	cmds := []tea.Cmd{m.spinner.Tick}
	// Arriving at a tab that paused for review while in the background:
	// land on the call, as if the pause had happened in view.
	if m.session.State() == session.StateAwaitingReview && m.input.Value() == "" {
		if toolCallID := m.reviewTarget(); toolCallID != "" {
			m.focusToolCall(toolCallID)
			return tea.Batch(cmds...)
		}
	}
	if m.focusedComponent == FocusTextarea {
		cmds = append(cmds, m.input.Focus())
	}
	return tea.Batch(cmds...)
}

func (m *ChatScreen) OnBlur() {
	m.focused = false
	m.input.Blur()
}

// TabState is the activity the tab label signals, so background chats are
// noticed.
func (m *ChatScreen) TabState() widget.TabState {
	switch {
	case m.session.State() == session.StateAwaitingReview:
		return widget.TabReviewing
	case m.session.TurnInFlight():
		return widget.TabBusy
	}
	return m.unread
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
				m.input.SetValue(msg.Content)
				m.updateLayoutForInput()
			}
			return m.input.Focus()
		}
		return nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		m.timeline.Animate()
		return cmd

	case tea.KeyPressMsg:
		// The info modal is read-only: any key dismisses it, and none reach
		// the chat underneath.
		if m.info != nil {
			m.info = nil
			return nil
		}
		if m.document != nil {
			if key.Matches(msg, timeline.KeyViewRequest.Key) || !m.document.HandleKey(msg) {
				m.document = nil
			}
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
		cmds = append(cmds, m.toast(AlertError, err.Error()))
	}
	if _, ok := event.(session.RefreshEvent); ok {
		m.noteTurnState()
		m.refresh()
		cmds = append(cmds, m.maybeJumpToReview())
	}
	return tea.Batch(cmds...)
}

// alert surfaces text as an informational toast.
func (m *ChatScreen) alert(text string) tea.Cmd {
	return m.toast(AlertInfo, text)
}

func (m *ChatScreen) toast(level AlertLevel, text string) tea.Cmd {
	wrap := m.wrap
	return func() tea.Msg { return wrap(AlertMsg{Text: text, Level: level}) }
}

// Interrupt is ctrl+c short of quitting: it closes a modal, cancels the
// running turn, or clears the input, and reports whether it did any. Each
// must be visible, or the press reads as ignored.
func (m *ChatScreen) Interrupt() bool {
	switch {
	case m.info != nil:
		m.info = nil
	case m.document != nil:
		m.document = nil
	case m.picker != nil:
		m.picker, m.pickerApply = nil, nil
	case m.session.TurnInFlight() && !m.cancelling:
		// CancelTurn aborts the turn wherever it is (streaming, tools,
		// review) and it resolves cleanly: cancelled results, inputs
		// re-queued.
		m.session.CancelTurn()
		m.cancelling = true
	case m.input.Value() != "":
		m.input.Reset()
		m.updateLayoutForInput()
	default:
		// Whitespace alone is invisible: clear it, but don't count it.
		m.input.Reset()
		m.updateLayoutForInput()
		return false
	}
	return true
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
			m.updateLayoutForInput()
			m.session.RejectToolCall(toolCallID, reason)
			return nil
		})
	case key.Matches(msg, chatKeyCycleReasoning.Key):
		m.session.CycleReasoningEffort()
		effort := session.ReasoningLabel(m.session.Params().ReasoningEffort)
		if effort == "" {
			effort = "model default"
		}
		// The status bar's reasoning fact is among the first to drop when narrow.
		return m.alert("Reasoning effort: " + effort)
	case key.Matches(msg, chatKeyToggleFavorite.Key):
		return m.toggleFavorite()
	case key.Matches(msg, chatKeyOpenAll.Key):
		return editor.Open(timeline.ConversationText(m.session.Messages(), m.session.Registry()), "md")
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
	case key.Matches(msg, chatKeyFocusLauncher.Key):
		return m.focusLauncher()
	case key.Matches(msg, chatKeyNextAgent.Key):
		return m.focusNextAgent()
	case key.Matches(msg, chatKeyInfo.Key):
		// Snapshotted on open: the modal is a still frame, so a streaming
		// turn never mutates the numbers under the reader's eyes.
		m.info = m.session.Info()
		return nil
	case key.Matches(msg, timeline.KeyViewRequest.Key):
		return m.viewRequest()
	}

	switch {
	case key.Matches(msg, chatKeySubmit.Key):
		return m.submit()
	case key.Matches(msg, chatKeyPageUp.Key):
		m.timeline.ScrollBy(-max(1, m.timeline.Height()-2))
		return nil
	case key.Matches(msg, chatKeyPageDown.Key):
		m.timeline.ScrollBy(max(1, m.timeline.Height()-2))
		return nil
	}

	// Timeline stays navigable at all times — including during tool review.
	if m.focusedComponent == FocusViewport {
		switch {
		case key.Matches(msg, chatKeyBack.Key):
			return m.focusInput()
		case widget.IsHistoryKey(msg):
			// History browsing is for the input wherever focus is.
			focus := m.focusInput()
			m.input.HandleKey(msg)
			m.updateLayoutForInput()
			return focus
		}
		return m.timeline.HandleKey(msg, m.alert)
	}

	if cmd, ok := m.input.HandleKey(msg); ok {
		m.updateLayoutForInput()
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
		cmds = append(cmds, m.alert("Message queued · the tool call still awaits your verdict"))
	}
	cmds = append(cmds, m.spinner.Tick, func() tea.Msg {
		// Queues an interjection when a turn is in flight; starts a turn
		// otherwise. Refreshes arrive on the session event channel.
		sess.SendMessage(text)
		return nil
	})
	return tea.Batch(cmds...)
}

// reviewTarget returns the tool call a verdict applies to: the selected one
// when navigating the timeline, otherwise any pending call. Empty when nothing
// is awaiting review.
func (m *ChatScreen) reviewTarget() string {
	// One snapshot: sub-agents resolve reviews concurrently.
	pending := m.session.PendingReviews()
	if len(pending) == 0 {
		return ""
	}
	if m.focusedComponent == FocusViewport {
		if item, ok := m.timeline.SelectedItem().(*timeline.ToolCallItem); ok {
			for _, request := range pending {
				if request.ToolCall.GetId() == item.ToolCall.GetId() {
					return request.ToolCall.GetId()
				}
			}
		}
	}
	// Oldest first: sub-agents may each park a call, and the hint and the
	// verdict must name the same one.
	return pending[0].ToolCall.GetId()
}

// withReviewTarget applies a verdict to the current review target, if any.
func (m *ChatScreen) withReviewTarget(verdict func(toolCallID string) tea.Cmd) tea.Cmd {
	toolCallID := m.reviewTarget()
	if toolCallID == "" {
		return nil
	}
	return verdict(toolCallID)
}

// viewRequest opens the full request of the selected tool call, else of the
// call awaiting review, so a folded request is readable from the input too.
func (m *ChatScreen) viewRequest() tea.Cmd {
	item, ok := m.timeline.SelectedItem().(*timeline.ToolCallItem)
	if !ok || m.focusedComponent != FocusViewport {
		item = m.toolCallItem(m.reviewTarget())
	}
	if item == nil {
		return m.alert("Select a tool call to view its request")
	}
	title, md := item.Request()
	m.document = widget.NewDocument(title, md, m.width, m.height)
	return nil
}

// toolCallItem finds a tool call's timeline item; nil when absent.
func (m *ChatScreen) toolCallItem(toolCallID string) *timeline.ToolCallItem {
	if toolCallID == "" {
		return nil
	}
	for _, item := range m.buildItems() {
		if toolCallItem, ok := item.(*timeline.ToolCallItem); ok && toolCallItem.ToolCall.GetId() == toolCallID {
			return toolCallItem
		}
	}
	return nil
}

// toolNameForCall resolves a tool call ID to its tool name via the timeline.
func (m *ChatScreen) toolNameForCall(toolCallID string) string {
	if item := m.toolCallItem(toolCallID); item != nil {
		return item.ToolCall.GetName()
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
			return wrap(AlertMsg{Text: fmt.Sprintf("Delete failed: %v", err), Level: AlertError})
		}
		return wrap(AlertMsg{Text: "Message deleted", Level: AlertSuccess})
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
			return wrap(AlertMsg{Text: fmt.Sprintf("Delete failed: %v", err), Level: AlertError})
		}
		return wrap(AlertMsg{Text: fmt.Sprintf("%d messages deleted", count), Level: AlertSuccess})
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
		return m.alert("Input has unsent text · clear it first")
	}

	// Text lands in the input immediately; the truncation runs off the UI
	// loop, and a failure still leaves the user holding their text.
	m.timeline.ClearSelection()
	m.input.SetValue(text)
	m.updateLayoutForInput()
	focus := m.focusInput()
	sess := m.session
	wrap := m.wrap
	return tea.Batch(focus, func() tea.Msg {
		if _, err := sess.DeleteMessagesFrom(messageName); err != nil {
			return wrap(AlertMsg{Text: fmt.Sprintf("Edit failed: %v", err), Level: AlertError})
		}
		return nil
	})
}

// deletableSelection resolves the message a delete key acts on, or an alert
// explaining why nothing can be deleted right now.
func (m *ChatScreen) deletableSelection(keyHint string) (string, tea.Cmd) {
	if m.focusedComponent != FocusViewport {
		return "", m.alert(fmt.Sprintf("Deleting a message needs timeline focus · tab to navigate, then %s", keyHint))
	}
	// A turn in flight is still appending to (and persisting) the history;
	// deleting underneath it would race those writes. TurnInFlight, not Busy:
	// a review pause is still mid-turn.
	if m.session.TurnInFlight() {
		return "", m.alert("Cannot delete while the turn is running · ctrl+c to cancel first")
	}
	messageName := m.timeline.SelectedMessageName()
	if messageName == "" {
		return "", m.alert("No deletable message selected")
	}
	return messageName, nil
}

// maybeJumpToReview announces each newly pending review: a focused chat lands
// on the call, a background one alerts.
func (m *ChatScreen) maybeJumpToReview() tea.Cmd {
	pending := m.session.PendingReviews()
	seen := make(map[string]bool, len(pending))
	var fresh []*review.Request
	for _, request := range pending {
		toolCallID := request.ToolCall.GetId()
		seen[toolCallID] = true
		if !m.seenReviews[toolCallID] {
			fresh = append(fresh, request)
		}
	}
	m.seenReviews = seen
	if len(fresh) == 0 {
		return nil
	}
	// Don't steal focus mid-composition: blurring the input silently drops
	// keystrokes into the timeline while the user is typing a message.
	if m.focused && m.input.Value() == "" {
		if !m.pendingReviewSelected(seen) {
			m.focusToolCall(fresh[0].ToolCall.GetId())
		}
		return nil
	}
	// A sub-agent's review also surfaces in the root chat, which alerts.
	if !m.focused && m.session.ReviewsEscalate() {
		return nil
	}
	cmds := make([]tea.Cmd, len(fresh))
	for i, request := range fresh {
		origin := request.Origin
		if origin == "" {
			origin = m.Title()
		}
		cmds[i] = m.alert(fmt.Sprintf("%s: tool call awaits your review", origin))
	}
	return tea.Batch(cmds...)
}

// pendingReviewSelected reports whether the timeline cursor already rests on
// a call awaiting review — the user is mid-verdict, so a newer one waits.
func (m *ChatScreen) pendingReviewSelected(pending map[string]bool) bool {
	if m.focusedComponent != FocusViewport {
		return false
	}
	item, ok := m.timeline.SelectedItem().(*timeline.ToolCallItem)
	return ok && pending[item.ToolCall.GetId()]
}

// noteTurnState times a turn from its start, and records how it ended out
// of sight, until the tab is focused.
func (m *ChatScreen) noteTurnState() {
	inFlight := m.session.TurnInFlight()
	started, ended := !m.turnInFlight && inFlight, m.turnInFlight && !inFlight
	m.turnInFlight = inFlight
	if started {
		m.turnStart = time.Now()
	}
	if !inFlight {
		m.cancelling = false
	}
	if !ended || m.focused {
		return
	}
	m.unread = widget.TabDone
	if err := m.session.StreamError(); err != nil && !session.IsCancelError(err) {
		m.unread = widget.TabFailed
	}
}

// focusLauncher asks for the tab of the agent that launched this chat.
func (m *ChatScreen) focusLauncher() tea.Cmd {
	var launcher tool.Agent
	if m.agents != nil {
		launcher = m.agents.Parent(m.session)
	}
	if launcher == nil {
		return m.alert("This chat was not launched by an agent")
	}
	return focusAgent(launcher, agentTitle(launcher))
}

// focusNextAgent asks for the tab of the next sub-agent, those awaiting
// review first.
func (m *ChatScreen) focusNextAgent() tea.Cmd {
	children := m.children()
	if len(children) == 0 {
		return m.alert("This chat has no sub-agents")
	}
	m.agentCursor = nextAgent(children, m.agentCursor)
	child := children[m.agentCursor]
	if child.Agent == nil {
		return m.alert(fmt.Sprintf("%s has not launched yet", child.Title))
	}
	return focusAgent(child.Agent, child.Title)
}

// nextAgent picks the sub-agent after index after, preferring one awaiting
// review; otherwise the next in launch order.
func nextAgent(children []agent.Status, after int) int {
	for i := 1; i <= len(children); i++ {
		if j := (after + i) % len(children); children[j].State == sgptpb.AgentOperation_STATE_AWAITING_REVIEW {
			return j
		}
	}
	return (after + 1) % len(children)
}

func focusAgent(target tool.Agent, title string) tea.Cmd {
	return func() tea.Msg { return FocusAgentMsg{Agent: target, Title: title} }
}

// agentTitle names an agent's chat, untitled until its first exchange.
func agentTitle(target tool.Agent) string {
	if title := target.Chat().GetTitle(); title != "" {
		return title
	}
	return "launcher"
}

func (m *ChatScreen) children() []agent.Status {
	if m.agents == nil {
		return nil
	}
	return m.agents.Children(m.session)
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

// focusTimeline lands the selection where attention belongs: the call
// awaiting review, else the latest item.
func (m *ChatScreen) focusTimeline() {
	if toolCallID := m.reviewTarget(); toolCallID != "" {
		m.focusToolCall(toolCallID)
		if m.timeline.SelectedItem() != nil {
			return
		}
	}
	m.focusedComponent = FocusViewport
	m.input.Blur()
	m.timeline.SetFocused(true)
	m.timeline.SelectLast()
}

func (m *ChatScreen) focusInput() tea.Cmd {
	m.focusedComponent = FocusTextarea
	m.timeline.SetFocused(false)
	return m.input.Focus()
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
		return wrap(AlertMsg{Text: fmt.Sprintf("Chat %s favorites", label), Level: AlertSuccess})
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
	m.picker = widget.NewPicker("Tools", items)
	m.picker.SetSize(m.width, m.height)
	sess := m.session
	wrap := m.wrap
	m.pickerApply = func(selected []string) tea.Cmd {
		return func() tea.Msg {
			// May dial a tool engine on first enablement — off the UI loop.
			if err := sess.SetEnabledTools(selected); err != nil {
				return wrap(AlertMsg{Text: fmt.Sprintf("Enabling tools failed: %v", err), Level: AlertError})
			}
			return wrap(AlertMsg{Text: fmt.Sprintf("Tools enabled: %d", len(selected)), Level: AlertSuccess})
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
	m.picker = widget.NewPicker("Files", items)
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
	if m.focusedComponent == FocusViewport {
		return m.focusInput()
	}
	m.focusTimeline()
	return nil
}

func (m *ChatScreen) updateInput(msg tea.Msg) tea.Cmd {
	cmd := m.input.Update(msg)
	m.updateLayoutForInput()
	return cmd
}

// updateLayoutForInput relayouts when the input grew or shrank.
func (m *ChatScreen) updateLayoutForInput() {
	if m.input.Height() != m.lastInputHeight {
		m.lastInputHeight = m.input.Height()
		m.recalculateLayout()
	}
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
	// Sub-agents' calls awaiting a verdict here: answered like this chat's own.
	for _, request := range m.session.EscalatedReviews() {
		items = append(items, timeline.NewEscalatedToolCallItem(request.ToolCall, request.Origin, m.session.Registry()))
	}
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
		items = append(items, timeline.NewQueuedItem(fmt.Sprintf("queued-%d", i), queuedMessage))
	}
	return items
}

// Restyle re-renders against the palette the terminal's background implies.
func (m *ChatScreen) Restyle() {
	m.timeline.Restyle()
	m.refresh()
}

func (m *ChatScreen) refresh() {
	wasAtBottom := m.timeline.AtBottom()
	items := m.buildItems()
	m.timeline.SetLive(m.session.TurnInFlight())
	m.timeline.SetItems(items)
	m.activeTool, m.activeTitled = m.resolveActiveTool(items)
	m.refreshPlaceholder()
	m.recalculateLayout()
	if wasAtBottom {
		m.timeline.GotoBottom()
	}
}

func (m *ChatScreen) refreshPlaceholder() {
	switch {
	case m.reviewTarget() != "":
		m.input.SetPlaceholder("Message, or a reason to reject…")
	case m.session.TurnInFlight():
		m.input.SetPlaceholder("Queue a message…")
	default:
		m.input.SetPlaceholder("Message sgpt…")
	}
}

// resolveActiveTool names the call under review, else the one running: by
// the title the model gave it, else by its verb.
func (m *ChatScreen) resolveActiveTool(items []timeline.Item) (string, bool) {
	toolCallID := m.reviewTarget()
	if toolCallID == "" {
		toolCallID = m.session.ExecutingToolCallID()
	}
	if toolCallID == "" {
		return "", false
	}
	for _, item := range items {
		if toolCallItem, ok := item.(*timeline.ToolCallItem); ok && toolCallItem.ToolCall.GetId() == toolCallID {
			if title := tool.Title(toolCallItem.ToolCall); title != "" {
				return title, true
			}
			if summary, ok := m.session.Registry().Summarize(toolCallItem.ToolCall, nil); ok && summary.Verb != "" {
				return summary.Verb, false
			}
			return toolCallItem.ToolCall.GetName(), false
		}
	}
	return "", false
}

func (m *ChatScreen) recalculateLayout() {
	if m.width == 0 || m.height == 0 {
		return
	}

	// The line between the timeline and the input holds the agent strip, or
	// stays blank as breathing room.
	m.showAgents = len(m.children()) > 0 || (m.agents != nil && m.agents.Parent(m.session) != nil)
	m.input.SetWidth(m.width)
	m.input.SetMaxHeight(m.height - minTimelineHeight - 1)
	m.lastInputHeight = m.input.Height()
	m.timeline.SetSize(m.width, max(1, m.height-m.input.Height()-1))

	if !m.ready {
		m.ready = true
		m.refresh()
		m.timeline.GotoBottom()
	}
}

// Status is the chat's side of the status bar: the turn state, the hints
// that apply right now, and the session facts.
func (m *ChatScreen) Status() widget.Status {
	return widget.Status{
		State: m.turnState(),
		Below: m.timeline.LinesBelow(),
		Hints: m.hints(),
		Facts: m.facts(),
	}
}

func (m *ChatScreen) turnState() string {
	if m.cancelling {
		return m.spinner.View() + " " + styles.TextStyle.Render("Cancelling"+styles.GlyphEllipsis)
	}
	state := m.session.State()
	if state == session.StateAwaitingReview {
		// No spinner: nothing moves until the user answers.
		label := styles.GlyphReview + " Review"
		if m.activeTool != "" {
			label += " " + styles.GlyphSeparator + " " + m.activeTool
		}
		return reviewStateStyle.Render(label)
	}
	if !m.session.TurnInFlight() {
		return styles.MutedStyle.Render("Ready")
	}
	label := "Thinking"
	switch {
	case state == session.StateExecutingTools && m.activeTitled:
		label = m.activeTool
	case state == session.StateExecutingTools && m.activeTool != "":
		label = "Running " + m.activeTool
	case streamsText(m.session.StreamingMessage()):
		label = "Streaming"
	}
	elapsed := ""
	if !m.turnStart.IsZero() {
		elapsed = styles.MutedStyle.Render(" " + styles.GlyphSeparator + " " + format.Duration(time.Since(m.turnStart)))
	}
	return m.spinner.View() + " " + styles.TextStyle.Render(label) + elapsed
}

// streamsText reports whether the model has started its visible answer.
func streamsText(message *aipb.Message) bool {
	for _, block := range message.GetBlocks() {
		if block.GetText() != "" {
			return true
		}
	}
	return false
}

func (m *ChatScreen) hints() []string {
	switch {
	case m.reviewTarget() != "":
		return []string{
			chatKeySubmit.WithShort("accept").Hint(), chatKeyReject.WithShort("reject").Hint(),
			chatKeyAlwaysAccept.WithShort("always").Hint(), chatKeyCancel.WithShort("cancel").Hint(),
		}
	case m.session.TurnInFlight():
		return []string{chatKeyCancel.WithShort("cancel").Hint(), chatKeySubmit.WithShort("queue").Hint()}
	case m.focusedComponent == FocusViewport:
		return []string{timeline.KeyToggleCollapse.WithShort("expand").Hint(), chatKeyBack.WithShort("input").Hint(), chatKeyEditMessage.WithShort("edit").Hint()}
	}
	return []string{chatKeySubmit.WithShort("send").Hint(), chatKeyCycleFocus.WithShort("timeline").Hint()}
}

func (m *ChatScreen) facts() []widget.Fact {
	params := m.session.Params()
	var facts []widget.Fact
	if role := params.Role; role != nil && m.env.DefaultRole != "" &&
		role.GetName() != m.env.DefaultRole && role.GetAlias() != m.env.DefaultRole {
		name := role.GetAlias()
		if name == "" {
			name = role.GetName()
		}
		facts = append(facts, widget.Fact{Text: styles.MutedStyle.Render(name), Priority: 1})
	}
	modelName := &aipb.ModelRn{}
	if modelName.UnmarshalString(params.Model.GetName()) == nil {
		facts = append(facts, widget.Fact{Text: styles.MutedStyle.Render(modelName.Model), Priority: 4})
	}
	if effort := session.ReasoningLabel(params.ReasoningEffort); effort != "" {
		facts = append(facts, widget.Fact{Text: styles.MutedStyle.Render(effort), Priority: 2})
	}
	if limit := int64(params.Model.GetTtt().GetContextTokenLimit()); limit > 0 {
		last := m.session.LastModelUsage()
		used := last.GetInputToken().GetQuantity() + last.GetInputTokenCacheRead().GetQuantity()
		if used > 0 {
			percent := used * 100 / limit
			style := styles.MutedStyle
			switch {
			case percent >= 90:
				style = contextAlertStyle
			case percent >= 75:
				style = contextWarnStyle
			}
			facts = append(facts, widget.Fact{Text: style.Render(fmt.Sprintf("%d%% ctx", percent)), Priority: 5})
		}
	}
	if price := m.session.Price(); price > 0 {
		facts = append(facts, widget.Fact{Text: styles.MutedStyle.Render(format.Price(price)), Priority: 3})
	}
	return facts
}

// agentStrip renders the chat's launcher and sub-agents with their live
// states.
func (m *ChatScreen) agentStrip() string {
	strip := widget.AgentStrip{
		LauncherHint: chatKeyFocusLauncher.WithShort("launcher").Hint(),
		ChildrenHint: chatKeyNextAgent.WithShort("next agent").Hint(),
	}
	if launcher := m.agents.Parent(m.session); launcher != nil {
		strip.Launcher = agentTitle(launcher)
	}
	for _, child := range m.children() {
		strip.Children = append(strip.Children, widget.AgentEntry{Title: child.Title, State: child.State})
		if child.Agent != nil {
			strip.Price += child.Agent.Price()
		}
	}
	return widget.RenderAgentStrip(strip, m.width)
}

func (m *ChatScreen) View() string {
	if !m.ready {
		return ""
	}
	if m.info != nil {
		return widget.RenderInfo(m.info, m.width, m.height)
	}
	if m.document != nil {
		return m.document.View()
	}
	if m.picker != nil {
		return m.picker.View()
	}

	var b strings.Builder
	b.WriteString(m.timeline.View())
	b.WriteString("\n")
	if m.showAgents {
		b.WriteString(m.agentStrip())
	}
	b.WriteString("\n")
	b.WriteString(m.input.View())
	return b.String()
}

var _ Screen = (*ChatScreen)(nil)
