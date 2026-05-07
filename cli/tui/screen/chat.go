package screen

import (
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	aipb "github.com/malonaz/core/genproto/ai/v1"

	cliservice "github.com/malonaz/sgpt/cli/cli_service"
	"github.com/malonaz/sgpt/cli/cli_service/session"
	"github.com/malonaz/sgpt/cli/tui/styles"
	"github.com/malonaz/sgpt/cli/tui/widget"
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
	keyCycleFocus     = key.NewBinding(key.WithKeys("tab"))
	keyCycleReasoning = key.NewBinding(key.WithKeys("alt+t"))
	keyForkChat       = key.NewBinding(key.WithKeys("alt+="))
)

type ChatScreen struct {
	session *session.Session
	wrap    WrapFunc
	send    SendFunc

	titlebar   *widget.TitleBar
	messages   *widget.Messages
	input      *widget.Input
	toolReview *widget.ToolReview
	spinner    spinner.Model

	lastInputHeight int

	injectedFiles []string

	width            int
	height           int
	ready            bool
	focused          bool
	focusedComponent FocusedComponent
}

func NewChatScreen(
	svc *cliservice.Service,
	wrap WrapFunc,
	send SendFunc,
	sess *session.Session,
	injectedFiles []string,
) *ChatScreen {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = styles.SpinnerStyle

	cs := &ChatScreen{
		session:          sess,
		wrap:             wrap,
		send:             send,
		titlebar:         widget.NewTitleBar(),
		messages:         widget.NewMessages(),
		input:            widget.NewInput(),
		toolReview:       widget.NewToolReview(),
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

func (m *ChatScreen) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.recalculateLayout()
}

func (m *ChatScreen) OnFocus() tea.Cmd {
	m.focused = true
	if m.focusedComponent == FocusTextarea && !m.session.IsStreaming() && !m.inToolReview() {
		return m.input.Focus()
	}
	return nil
}

func (m *ChatScreen) OnBlur() {
	m.focused = false
	m.input.Blur()
}

func (m *ChatScreen) IsStreaming() bool {
	return m.session.IsStreaming()
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
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.recalculateLayout()
		return nil

	case sessionEventMsg:
		cmds = append(cmds, m.handleSessionEvent(msg.event))
		cmds = append(cmds, m.listenForSessionEvents())
		return tea.Batch(cmds...)

	case widget.EditorClosedMsg:
		switch m.focusedComponent {
		case FocusTextarea:
			if msg.Modified {
				m.input.Textarea.SetValue(msg.Content)
				m.input.AdjustHeight()
			}
			return m.input.Focus()
		case FocusViewport:
			return nil
		}
		return nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return cmd

	case tea.KeyPressMsg:
		return m.handleKeyPress(msg)
	}

	if !m.session.IsStreaming() && !m.inToolReview() {
		cmd := m.input.Update(msg)
		if m.input.Height() != m.lastInputHeight {
			m.lastInputHeight = m.input.Height()
			m.recalculateLayout()
		}
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
}

func (m *ChatScreen) handleSessionEvent(event session.Event) tea.Cmd {
	switch e := event.(type) {
	case session.RefreshEvent:
		wasAtBottom := m.messages.AtBottom()
		m.refreshMessages()
		m.refreshTitle()
		m.refreshToolReview()
		m.recalculateLayout()
		if wasAtBottom {
			m.messages.GotoBottom()
		}

	case session.ErrorEvent:
		return func() tea.Msg { return m.wrap(AlertMsg{Text: e.Err.Error()}) }
	}

	return nil
}

func (m *ChatScreen) inToolReview() bool {
	return m.toolReview.Active()
}

func (m *ChatScreen) refreshToolReview() {
	pending := m.session.PendingToolCalls()
	m.toolReview.SetToolCalls(pending)
}

func (m *ChatScreen) handleKeyPress(msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case key.Matches(msg, keyCycleFocus):
		if !m.inToolReview() {
			return m.cycleFocus()
		}
	case key.Matches(msg, keyCycleReasoning):
		m.cycleReasoningEffort()
		return nil
	case key.Matches(msg, keyForkChat):
		return func() tea.Msg { return m.wrap(OpenChatMsg{Chat: m.session.Chat(), Fork: true}) }
	}

	if m.inToolReview() {
		return m.handleToolReviewKey(msg)
	}

	switch m.focusedComponent {
	case FocusTextarea:
		if cmd := m.input.HandleKey(msg); cmd != nil {
			return cmd
		}
	case FocusViewport:
		wrap := m.wrap
		alertFn := func(text string) tea.Cmd {
			return func() tea.Msg { return wrap(AlertMsg{Text: text}) }
		}
		if cmd := m.messages.HandleKey(msg, alertFn); cmd != nil {
			return cmd
		}
	}

	switch msg.String() {
	case "ctrl+c":
		if m.session.IsStreaming() {
			m.session.CancelStream()
			return nil
		}
		return func() tea.Msg { return CloseTabMsg{} }

	case "ctrl+j":
		if m.session.IsStreaming() {
			return nil
		}
		userInput := m.input.Value()
		if userInput != "" {
			text := m.input.Submit()
			m.messages.ResetNavigation()
			m.refreshMessages()
			m.messages.GotoBottom()
			m.recalculateLayout()

			sess := m.session
			wrap := m.wrap
			return tea.Batch(m.spinner.Tick, func() tea.Msg {
				sess.SendMessage(text)
				return wrap(sessionEventMsg{event: session.RefreshEvent{}})
			})
		}
	}

	if !m.session.IsStreaming() {
		cmd := m.input.Update(msg)
		if m.input.Height() != m.lastInputHeight {
			m.lastInputHeight = m.input.Height()
			m.recalculateLayout()
		}
		return cmd
	}
	return nil
}

func (m *ChatScreen) handleToolReviewKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		return func() tea.Msg { return CloseTabMsg{} }

	case "ctrl+n":
		m.toolReview.NextToolCall()
		return nil

	case "ctrl+p":
		m.toolReview.PrevToolCall()
		return nil

	case "ctrl+j":
		reason := m.toolReview.InputValue()
		if reason == "" {
			m.toolReview.AcceptCurrent()
		} else {
			m.toolReview.RejectCurrent(reason)
		}
		m.toolReview.ResetInput()

		if m.toolReview.AllResolved() {
			sess := m.session
			wrap := m.wrap
			m.recalculateLayout()
			return tea.Batch(m.spinner.Tick, func() tea.Msg {
				sess.ResolveToolCalls()
				return wrap(sessionEventMsg{event: session.RefreshEvent{}})
			})
		}
		return nil
	}

	cmd := m.toolReview.UpdateInput(msg)
	return cmd
}

func (m *ChatScreen) cycleFocus() tea.Cmd {
	switch m.focusedComponent {
	case FocusTextarea:
		m.focusedComponent = FocusViewport
		m.input.Blur()
		if m.messages.NavMessageIndex() == -1 {
			m.messages.NavigateToBottom()
		}
		m.messages.SetFocused(true)
		m.refreshMessages()
	case FocusViewport:
		m.focusedComponent = FocusTextarea
		m.messages.SetFocused(false)
		m.refreshMessages()
		return m.input.Focus()
	}
	return nil
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

func (m *ChatScreen) refreshMessages() {
	m.messages.SetData(widget.MessagesData{
		ChatMessages:     m.session.Chat().GetMetadata().GetMessages(),
		StreamingMessage: m.session.StreamingMessage(),
		StreamError:      m.session.StreamError(),
		InjectedFiles:    m.injectedFiles,
	})
}

func (m *ChatScreen) refreshTitle() {
	m.titlebar.Refresh(m.session.Params(), m.session.TotalModelUsage(), m.session.LastModelUsage())
}

func (m *ChatScreen) recalculateLayout() {
	if m.width == 0 || m.height == 0 {
		return
	}

	m.titlebar.SetWidth(m.width)
	titleView := m.titlebar.View()

	viewportHeight := m.height - m.titlebar.Height()
	if !m.session.IsStreaming() {
		if m.inToolReview() {
			viewportHeight -= m.toolReview.Height()
		} else {
			viewportHeight -= m.input.Height()
		}
	}
	if viewportHeight < styles.MinViewportHeight {
		viewportHeight = styles.MinViewportHeight
	}

	m.messages.SetSize(m.width, viewportHeight)
	m.input.SetWidth(m.width)
	m.toolReview.SetWidth(m.width)

	if !m.ready {
		m.ready = true
		m.refreshMessages()
		m.messages.GotoBottom()
	}

	_ = titleView
}

func (m *ChatScreen) View() string {
	if !m.ready {
		return "Initializing..."
	}

	var b strings.Builder
	b.WriteString(m.titlebar.View())
	b.WriteString("\n")
	b.WriteString(m.messages.View())

	if !m.session.IsStreaming() {
		b.WriteString("\n")
		if m.inToolReview() {
			b.WriteString(m.toolReview.View())
		} else {
			b.WriteString(m.input.View())
		}
	}

	return b.String()
}

var _ Screen = (*ChatScreen)(nil)
