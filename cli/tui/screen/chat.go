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

// sessionEventMsg wraps a session.Event as a tea.Msg.
type sessionEventMsg struct {
	event session.Event
}

var (
	keyCycleFocus     = key.NewBinding(key.WithKeys("tab"))
	keyCycleReasoning = key.NewBinding(key.WithKeys("alt+t"))
	keyForkChat       = key.NewBinding(key.WithKeys("alt+="))
)

// ChatScreen is a thin compositor that wires a Session to view widgets.
type ChatScreen struct {
	session *session.Session
	wrap    WrapFunc
	send    SendFunc

	titlebar *widget.TitleBar
	messages *widget.Messages
	input    *widget.Input
	spinner  spinner.Model

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
		spinner:          sp,
		injectedFiles:    injectedFiles,
		focusedComponent: FocusTextarea,
	}
	cs.refreshTitle()
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
	if m.focusedComponent == FocusTextarea && !m.session.IsStreaming() {
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
		if m.focusedComponent == FocusTextarea && msg.Modified {
			m.input.Textarea.SetValue(msg.Content)
			m.input.AdjustHeight()
		}
		return nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return cmd

	case tea.KeyPressMsg:
		return m.handleKeyPress(msg)
	}

	if !m.session.IsStreaming() {
		cmd := m.input.Update(msg)
		cmds = append(cmds, cmd)
	}

	return tea.Batch(cmds...)
}

func (m *ChatScreen) handleSessionEvent(event session.Event) tea.Cmd {
	switch e := event.(type) {
	case session.RefreshEvent:
		m.refreshMessages()
		m.refreshTitle()
		if m.messages.AtBottom() {
			m.messages.GotoBottom()
		}
		m.recalculateLayout()

	case session.ErrorEvent:
		return func() tea.Msg { return m.wrap(AlertMsg{Text: e.Err.Error()}) }
	}

	return nil
}

func (m *ChatScreen) handleKeyPress(msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case key.Matches(msg, keyCycleFocus):
		return m.cycleFocus()
	case key.Matches(msg, keyCycleReasoning):
		m.cycleReasoningEffort()
		return nil
	case key.Matches(msg, keyForkChat):
		return func() tea.Msg { return m.wrap(OpenChatMsg{Chat: m.session.Chat(), Fork: true}) }
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

		if len(m.session.PendingToolCalls()) > 0 {
			if userInput == "" {
				// Accept tool calls — runs blocking in a tea.Cmd goroutine.
				sess := m.session
				wrap := m.wrap
				m.recalculateLayout()
				return tea.Batch(m.spinner.Tick, func() tea.Msg {
					sess.AcceptToolCalls()
					return wrap(sessionEventMsg{event: session.RefreshEvent{}})
				})
			}
			m.input.Reset()
			sess := m.session
			wrap := m.wrap
			reason := userInput
			m.recalculateLayout()
			return tea.Batch(m.spinner.Tick, func() tea.Msg {
				sess.RejectAndResend(reason)
				return wrap(sessionEventMsg{event: session.RefreshEvent{}})
			})
		}

		if userInput != "" {
			text := m.input.Submit()
			m.messages.ResetNavigation()
			m.refreshMessages()
			m.messages.GotoBottom()
			m.recalculateLayout()
			// SendMessage blocks — run in a tea.Cmd goroutine.
			sess := m.session
			wrap := m.wrap
			return tea.Batch(m.spinner.Tick, func() tea.Msg {
				sess.SendMessage(text)
				return wrap(sessionEventMsg{event: session.RefreshEvent{}})
			})
		}
	}

	if !m.session.IsStreaming() {
		return m.input.Update(msg)
	}
	return nil
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
		viewportHeight -= m.input.Height()
	}
	if viewportHeight < styles.MinViewportHeight {
		viewportHeight = styles.MinViewportHeight
	}

	m.messages.SetSize(m.width, viewportHeight)
	m.input.SetWidth(m.width)

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
		b.WriteString(m.input.View())
		if len(m.session.PendingToolCalls()) > 0 {
			b.WriteString("\n")
			b.WriteString(styles.HelpStyle.Render("Tool call pending: Ctrl+J to accept, type + Ctrl+J to reject"))
		}
	}

	return b.String()
}

var _ Screen = (*ChatScreen)(nil)
