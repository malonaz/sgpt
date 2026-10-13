package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	aipb "github.com/malonaz/core/genproto/ai/v1"

	"github.com/malonaz/sgpt/cli/tui/clipboard"
	"github.com/malonaz/sgpt/cli/tui/keymap"
	"github.com/malonaz/sgpt/cli/tui/screen"
	menuscreen "github.com/malonaz/sgpt/cli/tui/screen/menu"
	"github.com/malonaz/sgpt/cli/tui/styles"
	"github.com/malonaz/sgpt/cli/tui/widget"
	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/history"
	"github.com/malonaz/sgpt/internal/session"
	"github.com/malonaz/sgpt/internal/store"
	"github.com/malonaz/sgpt/internal/tool"
	"github.com/malonaz/sgpt/internal/tool/agent"
)

const (
	menuTabID = "menu"
	// Errors stay up long enough to be read in full; the rest just confirm.
	toastDuration      = 2500 * time.Millisecond
	errorToastDuration = 6 * time.Second
	// chromeHeight is the tab bar, the blank line under it, and the status
	// bar.
	chromeHeight = 3
)

type alertDismissMsg struct{}

type openTabMsg struct {
	id     string
	screen screen.Screen
	// background opens the tab without focusing it.
	background bool
	// launcher, for a sub-agent's tab, places it in its launcher's subtree.
	launcher tool.Agent
}

// confirmation is a pending yes/no modal guarding an action.
type confirmation struct {
	title  string
	body   string
	action func() tea.Cmd
	// quit marks the quit confirm, which the quit keys accept rather than dismiss.
	quit bool
}

// agentDirectory serves the sub-agent tree to chat screens; the manager is
// set after the App is built, and may never be.
type agentDirectory struct {
	manager *agent.Manager
}

func (d *agentDirectory) Children(parent tool.Agent) []agent.Status {
	if d.manager == nil {
		return nil
	}
	return d.manager.Children(parent)
}

func (d *agentDirectory) Parent(child tool.Agent) tool.Agent {
	if d.manager == nil {
		return nil
	}
	return d.manager.Parent(child)
}

// AgentSessionFactory builds a ready-to-run session for a sub-agent launch
// (registry, injected files, system prompt, policy). Lives in cli/chat, which
// owns tool/file/role resolution.
type AgentSessionFactory func(ctx context.Context, request *agent.LaunchRequest) (*session.Session, error)

type tab struct {
	id     string
	screen screen.Screen
}

var (
	keyInterrupt      = keymap.New("ctrl+c", "Interrupt; quit once nothing is left to cancel")
	keyQuit           = keymap.New("ctrl+d", "Quit")
	keyNewTab         = keymap.New("ctrl+t", "New tab")
	keyCloseTab       = keymap.New("ctrl+w", "Close tab")
	keyCloseAgentTabs = keymap.New("alt+shift+w", "Close idle sub-agent tabs")
	keyHelpPageUp     = key.NewBinding(key.WithKeys("pgup"))
	keyHelpPageDown   = key.NewBinding(key.WithKeys("pgdown"))
	keyPrevTab        = keymap.New("alt+j", "Previous tab")
	keyNextTab        = keymap.New("alt+;", "Next tab")
	keyOpenMenu       = keymap.New("alt+m", "Open menu")
	keyCopyName       = keymap.New("alt+c", "Copy chat name")
	keyHelp           = keymap.New("alt+h", "Toggle help")
	keyTab1           = key.NewBinding(key.WithKeys("alt+f1"))
	keyTab2           = key.NewBinding(key.WithKeys("alt+f2"))
	keyTab3           = key.NewBinding(key.WithKeys("alt+f3"))
	keyTab4           = key.NewBinding(key.WithKeys("alt+f4"))
	keyTab5           = key.NewBinding(key.WithKeys("alt+f5"))
	keyTab6           = key.NewBinding(key.WithKeys("alt+f6"))
	keyTab7           = key.NewBinding(key.WithKeys("alt+f7"))
	keyTab8           = key.NewBinding(key.WithKeys("alt+f8"))
	keyTab9           = key.NewBinding(key.WithKeys("alt+f9"))
)

var tabIndexKeys = []key.Binding{keyTab1, keyTab2, keyTab3, keyTab4, keyTab5, keyTab6, keyTab7, keyTab8, keyTab9}

type App struct {
	ctx      context.Context
	store    *store.Store
	registry *tool.Registry

	defaultParams session.Params

	agentSessionFactory AgentSessionFactory
	agents              *agentDirectory
	chatEnv             *screen.ChatEnv
	agentTabCounter     atomic.Int64
	// newTabCounter uniquifies tab IDs for chats not yet persisted.
	newTabCounter atomic.Int64

	tabs      []*tab
	activeTab int
	// busySpinner animates busy tabs and agents. The app owns it because
	// screen ticks reach only the active tab; it ticks only while some tab
	// is busy.
	busySpinner        spinner.Model
	busySpinnerTicking bool

	program *tea.Program
	width   int
	height  int
	ready   bool

	// Alerts are queued so that none are ever lost; they display one at a
	// time as status bar toasts.
	alertQueue   []screen.AlertMsg
	alertVisible bool
	helpVisible  bool
	helpPage     int
	// confirm, when non-nil, is a modal awaiting y/enter; any other key
	// dismisses it.
	confirm  *confirmation
	quitting bool

	panel agentPanel
}

func NewApp(
	ctx context.Context,
	chatStore *store.Store,
	registry *tool.Registry,
	chatSession *session.Session,
	params session.Params,
) *App {
	app := &App{
		busySpinner:   spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		ctx:           ctx,
		store:         chatStore,
		registry:      registry,
		defaultParams: params,
		agents:        &agentDirectory{},
	}
	app.chatEnv = &screen.ChatEnv{Agents: app.agents, History: history.Open()}

	menuScreen := menuscreen.New(ctx, chatStore, registry, app.makeWrap(menuTabID))

	tabID := app.tabIDForChat(chatSession.Chat())
	chatScreen := screen.NewChatScreen(app.makeWrap(tabID), app.makeSend(tabID), chatSession, app.chatEnv)

	app.tabs = []*tab{
		{id: menuTabID, screen: menuScreen},
		{id: tabID, screen: chatScreen},
	}
	app.activeTab = 1
	return app
}

func (a *App) SetProgram(p *tea.Program) {
	a.program = p
}

func (a *App) SetAgentSessionFactory(factory AgentSessionFactory) {
	a.agentSessionFactory = factory
}

// SetDefaultRole names the configured role, which the status bar leaves
// unsaid.
func (a *App) SetDefaultRole(role string) {
	a.chatEnv.DefaultRole = role
}

// SetAgentManager lets chat screens show and navigate the sub-agent tree.
func (a *App) SetAgentManager(manager *agent.Manager) {
	a.agents.manager = manager
}

// LaunchAgent implements agent.Launcher: it opens the sub-agent's tab in
// the background — a batch would otherwise yank focus once per task while the
// user watches the launcher. Called from a tool goroutine, never the
// bubbletea loop, so the tab is opened via program.Send.
func (a *App) LaunchAgent(ctx context.Context, request *agent.LaunchRequest) (tool.Agent, error) {
	if a.agentSessionFactory == nil {
		return nil, fmt.Errorf("sub-agent launching is not configured")
	}
	chatSession, err := a.agentSessionFactory(ctx, request)
	if err != nil {
		return nil, err
	}
	// Chat names are assigned lazily on first save; use a local counter for a
	// unique tab ID.
	tabID := fmt.Sprintf("agent-%d", a.agentTabCounter.Add(1))
	chatScreen := screen.NewChatScreen(a.makeWrap(tabID), a.makeSend(tabID), chatSession, a.chatEnv)
	a.program.Send(openTabMsg{id: tabID, screen: chatScreen, background: true, launcher: request.Caller.Agent})
	return chatSession, nil
}

var _ agent.Launcher = (*App)(nil)

func (a *App) Init() tea.Cmd {
	cmds := []tea.Cmd{tea.RequestBackgroundColor}
	for i, t := range a.tabs {
		cmds = append(cmds, t.screen.Init())
		if i == a.activeTab {
			cmds = append(cmds, t.screen.OnFocus())
		}
	}
	return tea.Batch(cmds...)
}

func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if tick, ok := msg.(spinner.TickMsg); ok && tick.ID == a.busySpinner.ID() {
		a.busySpinnerTicking = a.anyTabBusy()
		if !a.busySpinnerTicking {
			return a, nil
		}
		var cmd tea.Cmd
		a.busySpinner, cmd = a.busySpinner.Update(tick)
		return a, cmd
	}
	model, cmd := a.update(msg)
	if !a.busySpinnerTicking && a.anyTabBusy() {
		a.busySpinnerTicking = true
		cmd = tea.Batch(cmd, a.busySpinner.Tick)
	}
	return model, cmd
}

func (a *App) anyTabBusy() bool {
	return slices.ContainsFunc(a.tabs, func(t *tab) bool {
		chatScreen, ok := t.screen.(*screen.ChatScreen)
		return ok && chatScreen.TabState() == widget.TabBusy
	})
}

func (a *App) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case alertDismissMsg:
		// Drop the alert that just finished displaying, then show the next.
		if len(a.alertQueue) > 0 {
			a.alertQueue = a.alertQueue[1:]
		}
		return a, a.displayNextAlert()

	case openTabMsg:
		return a, a.addTab(msg)

	case tea.BackgroundColorMsg:
		styles.SetBackground(msg.Color, msg.IsDark())
		for _, t := range a.tabs {
			if restyler, ok := t.screen.(screen.Restyler); ok {
				restyler.Restyle()
			}
		}
		return a, nil

	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
		a.ready = true
		// The panel's wearing follows the width: modal when narrow, else docked.
		if a.width >= panelMinWidth {
			a.panel.modal = false
		} else {
			a.panel.focused = false
		}
		a.layout()
		return a, nil

	case screen.TabMsg:
		for _, t := range a.tabs {
			if t.id == msg.TabID {
				switch innerMsg := msg.Msg.(type) {
				case screen.AlertMsg:
					return a, a.showAlert(innerMsg)
				case screen.ConfirmMsg:
					return a, a.requestConfirm(innerMsg)
				case screen.OpenChatMsg:
					return a, a.openChat(innerMsg)
				case screen.CloseTabMsg:
					return a, a.requestCloseTab(msg.TabID)
				default:
					cmd := t.screen.Update(innerMsg)
					return a, cmd
				}
			}
		}
		return a, nil

	case screen.AlertMsg:
		return a, a.showAlert(msg)

	case screen.ConfirmMsg:
		return a, a.requestConfirm(msg)

	case screen.OpenChatMsg:
		return a, a.openChat(msg)

	case screen.OpenMenuMsg:
		return a, a.focusMenu()

	case screen.CloseTabMsg:
		return a, a.requestCloseTab(msg.TabID)

	case screen.FocusAgentMsg:
		return a, a.focusAgent(msg)

	case tea.KeyPressMsg:
		// Modals swallow every key: y/enter confirms, anything else cancels.
		if confirm := a.confirm; confirm != nil {
			a.confirm = nil
			if key.Matches(msg, widget.KeyConfirm.Key) || confirm.quit && key.Matches(msg, keyQuit.Key, keyInterrupt.Key) {
				return a, confirm.action()
			}
			return a, nil
		}
		// alt+h opens help; pgup/pgdn page it and any other key closes it.
		if a.helpVisible {
			switch {
			case key.Matches(msg, keyHelpPageUp):
				a.helpPage = max(0, a.helpPage-1)
			case key.Matches(msg, keyHelpPageDown):
				_, pages := keymap.RenderHelpPage(a.keymaps(), a.width, a.height, 0)
				a.helpPage = min(a.helpPage+1, pages-1)
			default:
				a.helpVisible = false
			}
			return a, nil
		}
		if key.Matches(msg, keyHelp.Key) {
			a.helpVisible, a.helpPage = true, 0
			return a, nil
		}
		// Handled here so ctrl+d never reaches the textarea's delete-forward.
		if key.Matches(msg, keyQuit.Key) {
			return a, a.requestQuit()
		}
		if key.Matches(msg, keyTogglePanel.Key) {
			a.togglePanel()
			return a, nil
		}
		// The modal takes every key; the docked panel lets global keys through.
		switch {
		case a.panel.modal:
			return a, a.handlePanelKey(msg)
		case a.panelHasKeys():
			if cmd := a.handleGlobalKey(msg); cmd != nil {
				return a, cmd
			}
			return a, a.handlePanelKey(msg)
		case key.Matches(msg, screen.KeyCycleFocus.Key) && a.cycleFocusIntoPanel():
			return a, nil
		}
		if cmd := a.handleGlobalKey(msg); cmd != nil {
			return a, cmd
		}
	}

	if a.activeTab < len(a.tabs) {
		cmd := a.tabs[a.activeTab].screen.Update(msg)
		return a, cmd
	}
	return a, nil
}

func (a *App) View() tea.View {
	if a.quitting {
		return tea.NewView("")
	}
	if !a.ready {
		return tea.NewView("Initializing...")
	}

	if modal := a.modalView(); modal != "" {
		view := tea.NewView(modal)
		view.AltScreen = true
		view.ReportFocus = true
		return view
	}

	var b strings.Builder
	b.WriteString(a.renderTabBar())
	b.WriteString("\n\n")
	b.WriteString(a.renderContent())
	b.WriteString("\n")
	b.WriteString(a.renderStatusBar())

	view := tea.NewView(b.String())
	view.AltScreen = true
	// No mouse mode: native click-drag selection must keep working.
	view.ReportFocus = true
	return view
}

func (a *App) modalView() string {
	switch {
	case a.confirm != nil:
		return widget.RenderConfirm(a.confirm.title, a.confirm.body, a.width, a.height)
	case a.helpVisible:
		view, _ := keymap.RenderHelpPage(a.keymaps(), a.width, a.height, a.helpPage)
		return view
	case a.panel.modal:
		return widget.RenderAgentModal(a.agentPanelModel(), a.width, a.height)
	}
	return ""
}

// renderContent renders the active screen, with the agents panel docked on
// its right when there is room.
func (a *App) renderContent() string {
	content := a.tabs[a.activeTab].screen.View()
	if !a.panelDocked() {
		return content
	}
	// Placed in its full box so the panel lines up whatever the screen drew.
	content = lipgloss.Place(a.contentWidth(), a.contentHeight(), lipgloss.Left, lipgloss.Top, content)
	return lipgloss.JoinHorizontal(lipgloss.Top, content, widget.RenderAgentDock(a.agentPanelModel(), panelWidth, a.contentHeight()))
}

// keymaps composes the global bindings with the active screen's, so the help
// modal always reflects exactly what is currently reachable.
func (a *App) keymaps() []keymap.Map {
	maps := []keymap.Map{{
		Name: "Global",
		Bindings: []keymap.Binding{
			keyHelp, keyInterrupt, keyQuit, keyNewTab, keyCloseTab, keyCloseAgentTabs, keyPrevTab,
			keyNextTab, keyOpenMenu, keyCopyName, keyTogglePanel,
		},
	}, panelKeymap}
	if a.activeTab < len(a.tabs) {
		if keymapper, ok := a.tabs[a.activeTab].screen.(keymap.Keymapper); ok {
			maps = append(maps, keymapper.Keymaps()...)
		}
	}
	return maps
}

func (a *App) handleGlobalKey(msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case key.Matches(msg, keyInterrupt.Key):
		return a.interrupt()
	case key.Matches(msg, keyNewTab.Key):
		return a.createNewChat()
	case key.Matches(msg, keyCloseTab.Key):
		return a.requestCloseTab("")
	case key.Matches(msg, keyCloseAgentTabs.Key):
		return a.closeIdleAgentTabs()
	case key.Matches(msg, keyNextTab.Key):
		return a.switchTab(a.activeTab + 1)
	case key.Matches(msg, keyPrevTab.Key):
		return a.switchTab(a.activeTab - 1)
	case key.Matches(msg, keyOpenMenu.Key):
		return a.focusMenu()
	case key.Matches(msg, keyCopyName.Key):
		if chatScreen := a.activeChat(); chatScreen != nil {
			if chatName := chatScreen.Session().Chat().GetName(); chatName != "" {
				return tea.Batch(clipboard.Write(chatName), a.showAlert(screen.AlertMsg{Text: "Copied chat name: " + chatName, Level: screen.AlertSuccess}))
			}
		}
		return nil
	}
	for i, k := range tabIndexKeys {
		if key.Matches(msg, k) {
			return a.switchTab(i)
		}
	}
	return nil
}

func (a *App) isMenuTab(index int) bool {
	return index >= 0 && index < len(a.tabs) && a.tabs[index].id == menuTabID
}

func (a *App) switchTab(index int) tea.Cmd {
	if index < 0 || index >= len(a.tabs) || index == a.activeTab {
		return nil
	}
	a.tabs[a.activeTab].screen.OnBlur()
	a.activeTab = index
	return a.tabs[a.activeTab].screen.OnFocus()
}

func (a *App) activeChat() *screen.ChatScreen {
	if a.activeTab < len(a.tabs) {
		chatScreen, _ := a.tabs[a.activeTab].screen.(*screen.ChatScreen)
		return chatScreen
	}
	return nil
}

// chats returns the chat screens of every open tab.
func (a *App) chats() []*screen.ChatScreen {
	var chats []*screen.ChatScreen
	for _, t := range a.tabs {
		if chatScreen, ok := t.screen.(*screen.ChatScreen); ok {
			chats = append(chats, chatScreen)
		}
	}
	return chats
}

func (a *App) tabIndex(tabID string) int {
	return slices.IndexFunc(a.tabs, func(t *tab) bool { return t.id == tabID })
}

// agentTabIndex finds the tab of an agent's chat; -1 once it is closed.
func (a *App) agentTabIndex(target tool.Agent) int {
	return slices.IndexFunc(a.tabs, func(t *tab) bool {
		chatScreen, ok := t.screen.(*screen.ChatScreen)
		return ok && chatScreen.Session() == target
	})
}

func (a *App) tabDepth(index int) int {
	if chatScreen, ok := a.tabs[index].screen.(*screen.ChatScreen); ok {
		return chatScreen.Session().Params().Depth
	}
	return 0
}

func (a *App) focusAgent(msg screen.FocusAgentMsg) tea.Cmd {
	if index := a.agentTabIndex(msg.Agent); index >= 0 {
		return a.switchTab(index)
	}
	return a.showAlert(screen.AlertMsg{Text: fmt.Sprintf("%s: its tab was closed", msg.Title)})
}

// interrupt is ctrl+c: one press, one visible effect. The active screen
// stops what it can (modal, turn, input); otherwise the quit confirm opens.
func (a *App) interrupt() tea.Cmd {
	if interrupter, ok := a.tabs[a.activeTab].screen.(screen.Interrupter); ok && interrupter.Interrupt() {
		return nil
	}
	return a.requestQuit()
}

// requestConfirm guards a screen's action behind the confirm modal.
func (a *App) requestConfirm(msg screen.ConfirmMsg) tea.Cmd {
	a.confirm = &confirmation{title: msg.Title, body: msg.Body, action: func() tea.Cmd { return msg.Action }}
	return nil
}

// requestQuit asks before quitting: never exit on a single keystroke.
func (a *App) requestQuit() tea.Cmd {
	inFlight := 0
	for _, chatScreen := range a.chats() {
		if chatScreen.Session().TurnInFlight() {
			inFlight++
		}
	}
	body := "No chat has a turn in flight."
	switch {
	case inFlight == 1:
		body = "1 chat still has a turn in flight; quitting cancels it."
	case inFlight > 1:
		body = fmt.Sprintf("%d chats still have a turn in flight; quitting cancels them.", inFlight)
	}
	a.confirm = &confirmation{title: "Quit sgpt?", body: body, quit: true, action: func() tea.Cmd {
		a.quitting = true
		return tea.Quit
	}}
	return nil
}

// requestCloseTab closes a tab (the active one for an empty ID), asking first
// when that would quit or cut work short.
func (a *App) requestCloseTab(tabID string) tea.Cmd {
	index := a.activeTab
	if tabID != "" {
		index = a.tabIndex(tabID)
	}
	if index < 0 || a.isMenuTab(index) {
		return nil
	}
	if len(a.chats()) <= 1 {
		return a.requestQuit()
	}
	t := a.tabs[index]
	chatScreen, ok := t.screen.(*screen.ChatScreen)
	if !ok {
		return a.closeTab(t.id)
	}
	var work []string
	if chatScreen.Session().TurnInFlight() {
		work = append(work, "its turn is in flight")
	}
	if working := a.workingSubAgents(chatScreen.Session()); working > 0 {
		work = append(work, fmt.Sprintf("%d of its sub-agents are working", working))
	}
	if len(work) == 0 {
		return a.closeTab(t.id)
	}
	a.confirm = &confirmation{
		title:  fmt.Sprintf("Close %q?", t.screen.Title()),
		body:   "Closing cancels it: " + strings.Join(work, " and ") + ".",
		action: func() tea.Cmd { return a.closeTab(t.id) },
	}
	return nil
}

func (a *App) workingSubAgents(launcher tool.Agent) int {
	working := 0
	for _, child := range a.agents.Children(launcher) {
		switch child.State {
		case sgptpb.AgentOperation_STATE_QUEUED, sgptpb.AgentOperation_STATE_RUNNING, sgptpb.AgentOperation_STATE_AWAITING_REVIEW:
			working++
		}
	}
	return working
}

// closeIdleAgentTabs closes every sub-agent tab whose turn is over; their
// launchers can no longer message them.
func (a *App) closeIdleAgentTabs() tea.Cmd {
	var tabIDs []string
	for _, t := range a.tabs {
		if chatScreen, ok := t.screen.(*screen.ChatScreen); ok &&
			chatScreen.Session().Params().Depth > 0 && !chatScreen.Session().TurnInFlight() {
			tabIDs = append(tabIDs, t.id)
		}
	}
	if len(tabIDs) == 0 {
		return a.showAlert(screen.AlertMsg{Text: "No idle sub-agent tabs"})
	}
	cmds := make([]tea.Cmd, 0, len(tabIDs)+1)
	for _, tabID := range tabIDs {
		cmds = append(cmds, a.closeTab(tabID))
	}
	cmds = append(cmds, a.showAlert(screen.AlertMsg{Text: fmt.Sprintf("Closed %d sub-agent tabs", len(tabIDs)), Level: screen.AlertSuccess}))
	return tea.Batch(cmds...)
}

func (a *App) closeTab(tabID string) tea.Cmd {
	index := a.tabIndex(tabID)
	if index < 0 || a.isMenuTab(index) {
		return nil
	}
	closed := a.tabs[index].screen
	// The tab is the only handle on its session: left running it would
	// stream on unseen, or park forever on a review nobody can answer.
	if chatScreen, ok := closed.(*screen.ChatScreen); ok {
		chatScreen.Session().Close()
	}
	a.tabs = slices.Delete(a.tabs, index, index+1)
	switch {
	case index < a.activeTab:
		a.activeTab--
		return nil
	case index > a.activeTab:
		return nil
	}
	closed.OnBlur()
	if a.activeTab >= len(a.tabs) {
		a.activeTab = len(a.tabs) - 1
	}
	if a.isMenuTab(a.activeTab) && a.activeTab+1 < len(a.tabs) {
		a.activeTab++
	}
	return a.tabs[a.activeTab].screen.OnFocus()
}

func (a *App) addTab(msg openTabMsg) tea.Cmd {
	msg.screen.SetSize(a.contentWidth(), a.contentHeight())
	index := a.insertionIndex(msg.launcher)
	a.tabs = slices.Insert(a.tabs, index, &tab{id: msg.id, screen: msg.screen})
	if index <= a.activeTab {
		a.activeTab++
	}
	if msg.background {
		return msg.screen.Init()
	}
	a.tabs[a.activeTab].screen.OnBlur()
	a.activeTab = index
	return tea.Batch(msg.screen.Init(), msg.screen.OnFocus())
}

// insertionIndex places a sub-agent's tab after its launcher's subtree, so
// tabs read as a tree; any other tab goes last.
func (a *App) insertionIndex(launcher tool.Agent) int {
	index := a.agentTabIndex(launcher)
	if index < 0 {
		return len(a.tabs)
	}
	depth := a.tabDepth(index)
	for index++; index < len(a.tabs) && a.tabDepth(index) > depth; index++ {
	}
	return index
}

func (a *App) openChat(msg screen.OpenChatMsg) tea.Cmd {
	if msg.Chat != nil {
		for i, t := range a.tabs {
			// Compare against the live session chat: lazily-created chats
			// keep their placeholder tab ID after being persisted.
			if chatScreen, ok := t.screen.(*screen.ChatScreen); ok &&
				chatScreen.Session().Chat().GetName() == msg.Chat.Name {
				return a.switchTab(i)
			}
		}
	}

	return func() tea.Msg {
		chat := msg.Chat
		var err error

		if chat == nil {
			// Deferred creation: the session persists the chat on the first
			// send, so an opened-and-abandoned tab leaves no empty chat behind.
			chat = &aipb.Chat{}
			store.SetCurrentModel(chat, a.defaultParams.Model.Name)
		}

		// Messages are server-side resources: load the history to seed the
		// session (nothing to load for a not-yet-persisted chat).
		var messages []*aipb.Message
		if chat.Name != "" {
			messages, err = a.store.ListMessages(a.ctx, chat.Name)
			if err != nil {
				return screen.AlertMsg{Text: fmt.Sprintf("Loading messages failed: %v", err), Level: screen.AlertError}
			}
		}

		params := a.defaultParams
		params.Chat = chat.Name
		tabID := a.tabIDForChat(chat)

		chatSession := session.New(a.ctx, a.store, a.registry, chat, messages, params)
		chatScreen := screen.NewChatScreen(a.makeWrap(tabID), a.makeSend(tabID), chatSession, a.chatEnv)
		return openTabMsg{id: tabID, screen: chatScreen}
	}
}

func (a *App) createNewChat() tea.Cmd {
	return a.openChat(screen.OpenChatMsg{})
}

func (a *App) focusMenu() tea.Cmd {
	for i, t := range a.tabs {
		if t.id == menuTabID {
			return a.switchTab(i)
		}
	}
	return nil
}

func (a *App) showAlert(alert screen.AlertMsg) tea.Cmd {
	a.alertQueue = append(a.alertQueue, alert)
	if a.alertVisible {
		// Already displaying; the pending dismiss tick will pop the next one.
		return nil
	}
	return a.displayNextAlert()
}

func (a *App) displayNextAlert() tea.Cmd {
	if len(a.alertQueue) == 0 {
		a.alertVisible = false
		return nil
	}
	a.alertVisible = true
	duration := toastDuration
	if a.alertQueue[0].Level == screen.AlertError {
		duration = errorToastDuration
	}
	return tea.Tick(duration, func(time.Time) tea.Msg { return alertDismissMsg{} })
}

// tabIDForChat returns a routing ID for a tab: the chat's resource name, or a
// unique placeholder for chats not yet persisted (created lazily on first send).
func (a *App) tabIDForChat(chat *aipb.Chat) string {
	if name := chat.GetName(); name != "" {
		return name
	}
	return fmt.Sprintf("new-%d", a.newTabCounter.Add(1))
}

func (a *App) makeWrap(tabID string) screen.WrapFunc {
	return func(msg tea.Msg) tea.Msg {
		return screen.TabMsg{TabID: tabID, Msg: msg}
	}
}

func (a *App) makeSend(tabID string) screen.SendFunc {
	return func(msg tea.Msg) {
		if a.program != nil {
			a.program.Send(screen.TabMsg{TabID: tabID, Msg: msg})
		}
	}
}

func (a *App) contentHeight() int {
	return max(0, a.height-chromeHeight)
}

func (a *App) renderTabBar() string {
	tabs := make([]widget.Tab, len(a.tabs))
	for i, t := range a.tabs {
		tabs[i] = widget.Tab{Title: t.screen.Title(), Active: i == a.activeTab}
		if chatScreen, ok := t.screen.(*screen.ChatScreen); ok {
			tabs[i].State = chatScreen.TabState()
			tabs[i].Depth = chatScreen.Session().Params().Depth
			tabs[i].Spinner = a.busySpinner.View()
		}
	}
	return widget.RenderTabBar(tabs, a.width)
}

// renderStatusBar renders the active screen's status, the current toast in
// place of its hints, and help as the last hint.
func (a *App) renderStatusBar() string {
	var status widget.Status
	if reporter, ok := a.tabs[a.activeTab].screen.(screen.StatusReporter); ok {
		status = reporter.Status()
	}
	status.Hints = append(status.Hints, keyHelp.WithShort("help").Hint())
	var toast *widget.Toast
	if a.alertVisible && len(a.alertQueue) > 0 {
		alert := a.alertQueue[0]
		toast = &widget.Toast{Text: alert.Text, Level: toastLevels[alert.Level]}
	}
	return widget.RenderStatusBar(status, toast, a.width)
}

var toastLevels = map[screen.AlertLevel]widget.ToastLevel{
	screen.AlertInfo:    widget.ToastInfo,
	screen.AlertSuccess: widget.ToastSuccess,
	screen.AlertError:   widget.ToastError,
}
