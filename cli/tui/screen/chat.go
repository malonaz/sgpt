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
	"github.com/malonaz/sgpt/internal/session"
	"github.com/malonaz/sgpt/internal/tools"
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
	chatKeyCancel         = keymap.New("ctrl+c", "Cancel stream / close tab")
	chatKeyCycleReasoning = keymap.New("alt+t", "Cycle reasoning effort")
	chatKeyForkChat       = keymap.New("alt+=", "Fork chat")
	chatKeyToggleFavorite = keymap.New("alt+shift+f", "Toggle favorite")
	chatKeyOpenAll        = keymap.New("alt+shift+o", "Open entire chat in $EDITOR")
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

	injectedFiles   []string
	lastInputHeight int

	width            int
	height           int
	ready            bool
	focused          bool
	focusedComponent FocusedComponent
}

func NewChatScreen(
	wrap WrapFunc,
	send SendFunc,
	chatSession *session.Session,
	injectedFiles []string,
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
		injectedFiles:    injectedFiles,
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
	name := m.session.Chat().GetName()
	if name == "" {
		return "New Chat"
	}
	return strings.TrimPrefix(name, "chats/")
}

func (m *ChatScreen) ShortTitle() string {
	return styles.Truncate(m.Title(), 20)
}

func (m *ChatScreen) Keymaps() []keymap.Map {
	return []keymap.Map{
		{Name: "Chat", Bindings: []keymap.Binding{
			chatKeySubmit, chatKeyCancel, chatKeyCycleFocus,
			chatKeyCycleReasoning, chatKeyForkChat, chatKeyToggleFavorite,
			chatKeyOpenAll,
		}},
		timeline.Keymap(),
		widget.InputKeymap(),
	}
}

func (m *ChatScreen) SetSize(width, height int) {
	m.width = width
	m.height = height
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
		return m.handleKeyPress(msg)
	}

	if !m.session.Busy() && m.focusedComponent == FocusTextarea {
		return m.updateInput(msg)
	}
	return nil
}

func (m *ChatScreen) handleSessionEvent(event session.Event) tea.Cmd {
	switch e := event.(type) {
	case session.RefreshEvent:
		m.refresh()
	case session.ErrorEvent:
		return func() tea.Msg { return m.wrap(AlertMsg{Text: e.Err.Error()}) }
	}
	return nil
}

func (m *ChatScreen) handleKeyPress(msg tea.KeyPressMsg) tea.Cmd {
	wrap := m.wrap
	alertFn := func(text string) tea.Cmd {
		return func() tea.Msg { return wrap(AlertMsg{Text: text}) }
	}

	switch {
	case key.Matches(msg, chatKeyCycleFocus.Key):
		return m.cycleFocus()
	case key.Matches(msg, chatKeyCycleReasoning.Key):
		m.cycleReasoningEffort()
		return nil
	case key.Matches(msg, chatKeyForkChat.Key):
		return func() tea.Msg { return wrap(OpenChatMsg{Chat: m.session.Chat(), Fork: true}) }
	case key.Matches(msg, chatKeyToggleFavorite.Key):
		return m.toggleFavorite()
	case key.Matches(msg, chatKeyOpenAll.Key):
		return editor.Open(timeline.ConversationText(m.session.Chat().GetMetadata().GetMessages()), "md")
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
		return m.timeline.HandleKey(msg, alertFn)
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
	if m.session.Busy() {
		return nil
	}
	if pending := m.session.PendingToolCalls(); len(pending) > 0 {
		return m.reviewToolCall(pending)
	}

	text := m.input.Submit()
	if text == "" {
		return nil
	}
	m.timeline.ClearSelection()
	sess := m.session
	wrap := m.wrap
	m.refresh()
	m.timeline.GotoBottom()
	return tea.Batch(m.spinner.Tick, func() tea.Msg {
		sess.SendMessage(text)
		return wrap(sessionEventMsg{event: session.RefreshEvent{}})
	})
}

// reviewToolCall accepts (empty input) or rejects (input = reason) a tool
// call. Reviewing follows timeline selection: navigate onto any pending call
// to review it out of order; landing on an already-reviewed but not-yet
// executed call re-opens it so verdicts can be changed.
func (m *ChatScreen) reviewToolCall(pending []*aipb.ToolCall) tea.Cmd {
	target := pending[0]
	if m.focusedComponent == FocusViewport {
		if item, ok := m.timeline.SelectedItem().(*timeline.ToolCallItem); ok && item.Result == nil {
			switch tools.GetToolCallStatus(item.ToolCall) {
			case tools.ToolCallStatusPending:
				target = item.ToolCall
			case tools.ToolCallStatusAccepted, tools.ToolCallStatusRejected:
				tools.SetToolCallStatus(item.ToolCall, tools.ToolCallStatusPending)
				m.refresh()
				return nil
			}
		}
	}

	if reason := m.input.Value(); reason == "" {
		tools.SetToolCallStatus(target, tools.ToolCallStatusAccepted)
	} else {
		tools.SetToolCallStatus(target, tools.ToolCallStatusRejected)
		tools.SetToolCallRejectionReason(target, reason)
		m.input.Reset()
	}

	if len(m.session.PendingToolCalls()) > 0 {
		m.refresh()
		return nil
	}

	sess := m.session
	wrap := m.wrap
	m.refresh()
	return tea.Batch(m.spinner.Tick, func() tea.Msg {
		sess.ResolveToolCalls()
		return wrap(sessionEventMsg{event: session.RefreshEvent{}})
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
	var items []timeline.Item
	if len(m.injectedFiles) > 0 {
		items = append(items, timeline.NewInjectedFilesItem(m.injectedFiles))
	}
	items = append(items, m.builder.Build(
		m.session.Chat().GetMetadata().GetMessages(),
		m.session.StreamingMessage(),
		m.session.ExecutingToolCallID(),
		m.session.Registry(),
	)...)
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
	if pending := m.session.PendingToolCalls(); len(pending) > 0 {
		m.input.Textarea.Placeholder = fmt.Sprintf(
			"%d tool call(s) pending — ctrl+j: accept, type reason + ctrl+j: reject, tab: inspect", len(pending))
		return
	}
	m.input.Textarea.Placeholder = "Type your message... (ctrl+j: send, tab: navigate, alt+h: help)"
}

func (m *ChatScreen) refreshTitle() {
	m.titlebar.Refresh(m.session.Params(), m.session.TotalModelUsage(), m.session.LastModelUsage())
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
		return "executing tools..."
	}
	return "thinking... (ctrl+c to cancel)"
}

func (m *ChatScreen) View() string {
	if !m.ready {
		return "Initializing..."
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
