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
	return tea.Batch(textarea.Blink, m.spinner.Tick, m.listenForSessionEvents())
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
	if m.focusedComponent == FocusTextarea && !m.session.Busy() {
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
		if m.picker != nil {
			return m.handlePickerKey(msg)
		}
		return m.handleKeyPress(msg)
	}

	if !m.session.Busy() && m.focusedComponent == FocusTextarea {
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
	}

	switch msg.String() {
	case "ctrl+c":
		if m.session.IsStreaming() {
			m.session.CancelStream()
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
	if !m.session.Busy() {
		return m.updateInput(msg)
	}
	return nil
}

func (m *ChatScreen) submit() tea.Cmd {
	// Review takes precedence: a pending call means the turn is parked
	// waiting on this keystroke.
	if pendingToolCallID := m.reviewTarget(); pendingToolCallID != "" {
		return m.confirmReview(pendingToolCallID)
	}
	if m.session.Busy() {
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
	return tea.Batch(m.spinner.Tick, func() tea.Msg {
		sess.SendMessage(text)
		// Refreshes arrive on the session event channel; emitting one here
		// too would double-invoke maybeJumpToReview and can consume the
		// idle→review edge it keys off.
		return nil
	})
}

// confirmReview handles ctrl+j during review: it approves the target.
// Rejection is explicit (alt+shift+r) so composed text is never silently
// repurposed as a rejection reason.
func (m *ChatScreen) confirmReview(toolCallID string) tea.Cmd {
	if m.input.Value() != "" {
		return m.alert("Tool call pending review — ctrl+j/alt+y: accept, alt+shift+r: reject (input = reason)")
	}
	m.session.ApproveToolCall(toolCallID)
	return nil
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
		items = append(items, timeline.NewErrorItem("stream-error", err.Error()))
	}
	return items
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
	toolCallID := m.reviewTarget()
	if toolCallID == "" {
		m.input.Textarea.Placeholder = "Type your message... (ctrl+j: send, tab: navigate, alt+h: help)"
		return
	}
	m.input.Textarea.Placeholder = fmt.Sprintf(
		"reviewing %s — ctrl+j: accept, alt+shift+r: reject (input = reason), alt+shift+a: always accept this tool",
		m.toolNameForCall(toolCallID))
}

func (m *ChatScreen) refreshTitle() {
	m.titlebar.Refresh(m.session.Params(), m.session.TotalModelUsage(), m.session.LastModelUsage(), m.session.Price())
}

func (m *ChatScreen) recalculateLayout() {
	if m.width == 0 || m.height == 0 {
		return
	}

	m.titlebar.SetWidth(m.width)

	bottomHeight := m.input.Height()
	if m.session.Busy() {
		bottomHeight = 1
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
		return "executing tool..."
	}
	return "thinking... (ctrl+c to cancel)"
}

func (m *ChatScreen) View() string {
	if !m.ready {
		return "Initializing..."
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
	} else {
		b.WriteString(m.input.View())
	}
	return b.String()
}

var _ Screen = (*ChatScreen)(nil)
