package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"golang.design/x/clipboard"

	"github.com/malonaz/sgpt/cli/tui/keymap"
	"github.com/malonaz/sgpt/cli/tui/screen"
	menuscreen "github.com/malonaz/sgpt/cli/tui/screen/menu"
	"github.com/malonaz/sgpt/cli/tui/styles"
	"github.com/malonaz/sgpt/cli/tui/widget"
	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/session"
	"github.com/malonaz/sgpt/internal/store"
	"github.com/malonaz/sgpt/internal/tools"
)

const alertDuration = 2 * time.Second
const menuTabID = "menu"

type alertDismissMsg struct{}

type openTabMsg struct {
	id     string
	screen screen.Screen
}

type tab struct {
	id     string
	screen screen.Screen
}

var (
	keyQuit     = keymap.New("ctrl+c", "Quit")
	keyNewTab   = keymap.New("ctrl+t", "New chat tab")
	keyCloseTab = keymap.New("ctrl+w", "Close tab")
	keyPrevTab  = keymap.New("alt+j", "Previous tab")
	keyNextTab  = keymap.New("alt+;", "Next tab")
	keyOpenMenu = keymap.New("alt+m", "Open menu")
	keySearch   = keymap.New("ctrl+_", "Search chats")
	keyCopyName = keymap.New("alt+c", "Copy chat name")
	keyHelp     = keymap.New("alt+h", "Toggle this help")
	keyTab1     = key.NewBinding(key.WithKeys("alt+f1"))
	keyTab2     = key.NewBinding(key.WithKeys("alt+f2"))
	keyTab3     = key.NewBinding(key.WithKeys("alt+f3"))
	keyTab4     = key.NewBinding(key.WithKeys("alt+f4"))
	keyTab5     = key.NewBinding(key.WithKeys("alt+f5"))
	keyTab6     = key.NewBinding(key.WithKeys("alt+f6"))
	keyTab7     = key.NewBinding(key.WithKeys("alt+f7"))
	keyTab8     = key.NewBinding(key.WithKeys("alt+f8"))
	keyTab9     = key.NewBinding(key.WithKeys("alt+f9"))
)

var tabIndexKeys = []key.Binding{keyTab1, keyTab2, keyTab3, keyTab4, keyTab5, keyTab6, keyTab7, keyTab8, keyTab9}

type App struct {
	ctx      context.Context
	store    *store.Store
	registry *tools.Registry

	defaultParams session.Params

	tabs      []*tab
	activeTab int

	program *tea.Program
	width   int
	height  int
	ready   bool

	// Alerts are queued so that none are ever lost; they display one at a
	// time, each for alertDuration.
	alertQueue   []string
	alertVisible bool
	helpVisible  bool
	quitting     bool
}

func NewApp(
	ctx context.Context,
	chatStore *store.Store,
	registry *tools.Registry,
	initialChat *sgptpb.Chat,
	params session.Params,
) *App {
	app := &App{
		ctx:           ctx,
		store:         chatStore,
		registry:      registry,
		defaultParams: params,
	}

	menuScreen := menuscreen.New(ctx, chatStore, app.makeWrap(menuTabID))

	tabID := params.Chat
	chatSession := session.New(ctx, chatStore, registry, initialChat, params)
	chatScreen := screen.NewChatScreen(app.makeWrap(tabID), app.makeSend(tabID), chatSession, params.InjectedFiles)

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

func (a *App) Init() tea.Cmd {
	var cmds []tea.Cmd
	for i, t := range a.tabs {
		cmds = append(cmds, t.screen.Init())
		if i == a.activeTab {
			cmds = append(cmds, t.screen.OnFocus())
		}
	}
	return tea.Batch(cmds...)
}

func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case alertDismissMsg:
		// Drop the alert that just finished displaying, then show the next.
		if len(a.alertQueue) > 0 {
			a.alertQueue = a.alertQueue[1:]
		}
		return a, a.displayNextAlert()

	case openTabMsg:
		cmd := a.addTab(msg.id, msg.screen)
		return a, cmd

	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
		a.ready = true
		contentHeight := a.contentHeight()
		for _, t := range a.tabs {
			t.screen.SetSize(a.width, contentHeight)
		}
		return a, nil

	case screen.TabMsg:
		for _, t := range a.tabs {
			if t.id == msg.TabID {
				switch innerMsg := msg.Msg.(type) {
				case screen.AlertMsg:
					return a, a.showAlert(innerMsg.Text)
				case screen.OpenChatMsg:
					return a, a.openChat(innerMsg)
				case screen.CloseTabMsg:
					return a, a.closeTab(msg.TabID)
				default:
					cmd := t.screen.Update(innerMsg)
					return a, cmd
				}
			}
		}
		return a, nil

	case screen.AlertMsg:
		return a, a.showAlert(msg.Text)

	case screen.OpenChatMsg:
		return a, a.openChat(msg)

	case screen.OpenMenuMsg:
		return a, a.focusMenu()

	case screen.OpenSearchMsg:
		return a, a.focusMenuSearch()

	case screen.CloseTabMsg:
		return a, a.closeTab(msg.TabID)

	case tea.KeyPressMsg:
		// The help modal swallows every key: alt+h opens, anything closes.
		if a.helpVisible {
			a.helpVisible = false
			return a, nil
		}
		if key.Matches(msg, keyHelp.Key) {
			a.helpVisible = true
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

	if a.helpVisible {
		view := tea.NewView(keymap.RenderHelp(a.keymaps(), a.width, a.height))
		view.AltScreen = true
		view.ReportFocus = true
		return view
	}

	var b strings.Builder
	if a.alertVisible && len(a.alertQueue) > 0 {
		alertStyle := lipgloss.NewStyle().
			Background(styles.SuccessColor).
			Foreground(lipgloss.Color("#000000")).
			Bold(true).
			Padding(0, 1)
		b.WriteString(alertStyle.Width(a.width).Render(a.alertQueue[0]))
	} else {
		b.WriteString(a.renderTabBar())
	}
	b.WriteString("\n")
	if a.activeTab < len(a.tabs) {
		b.WriteString(a.tabs[a.activeTab].screen.View())
	}

	view := tea.NewView(b.String())
	view.AltScreen = true
	view.ReportFocus = true
	return view
}

// keymaps composes the global bindings with the active screen's, so the help
// modal always reflects exactly what is currently reachable.
func (a *App) keymaps() []keymap.Map {
	maps := []keymap.Map{{
		Name: "Global",
		Bindings: []keymap.Binding{
			keyHelp, keyQuit, keyNewTab, keyCloseTab, keyPrevTab, keyNextTab,
			keyOpenMenu, keySearch, keyCopyName,
		},
	}}
	if a.activeTab < len(a.tabs) {
		if keymapper, ok := a.tabs[a.activeTab].screen.(keymap.Keymapper); ok {
			maps = append(maps, keymapper.Keymaps()...)
		}
	}
	return maps
}

func (a *App) handleGlobalKey(msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case key.Matches(msg, keyQuit.Key):
		if a.activeTab < len(a.tabs) {
			if chatScreen, ok := a.tabs[a.activeTab].screen.(*screen.ChatScreen); ok && chatScreen.IsStreaming() {
				break
			}
		}
		a.quitting = true
		return tea.Quit
	case key.Matches(msg, keyNewTab.Key):
		return a.createNewChat()
	case key.Matches(msg, keyCloseTab.Key):
		return a.closeTab("")
	case key.Matches(msg, keyNextTab.Key):
		return a.switchTab(a.activeTab + 1)
	case key.Matches(msg, keyPrevTab.Key):
		return a.switchTab(a.activeTab - 1)
	case key.Matches(msg, keyOpenMenu.Key):
		return a.focusMenu()
	case key.Matches(msg, keySearch.Key):
		return a.focusMenuSearch()
	case key.Matches(msg, keyCopyName.Key):
		if a.activeTab < len(a.tabs) {
			if chatScreen, ok := a.tabs[a.activeTab].screen.(*screen.ChatScreen); ok {
				chatName := chatScreen.Session().Chat().GetName()
				if chatName != "" {
					clipboard.Write(clipboard.FmtText, []byte(chatName))
					return a.showAlert("Copied chat name: " + chatName)
				}
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

func (a *App) closeTab(tabID string) tea.Cmd {
	removeIndex := a.activeTab
	if tabID != "" {
		for i, t := range a.tabs {
			if t.id == tabID {
				removeIndex = i
				break
			}
		}
	}
	if a.isMenuTab(removeIndex) {
		return nil
	}
	nonMenuTabs := 0
	for _, t := range a.tabs {
		if t.id != menuTabID {
			nonMenuTabs++
		}
	}
	if nonMenuTabs <= 1 {
		a.quitting = true
		return tea.Quit
	}
	a.tabs[removeIndex].screen.OnBlur()
	a.tabs = append(a.tabs[:removeIndex], a.tabs[removeIndex+1:]...)
	if a.activeTab >= len(a.tabs) {
		a.activeTab = len(a.tabs) - 1
	}
	if a.isMenuTab(a.activeTab) && a.activeTab+1 < len(a.tabs) {
		a.activeTab++
	}
	return a.tabs[a.activeTab].screen.OnFocus()
}

func (a *App) addTab(id string, s screen.Screen) tea.Cmd {
	if a.activeTab < len(a.tabs) {
		a.tabs[a.activeTab].screen.OnBlur()
	}
	s.SetSize(a.width, a.contentHeight())
	a.tabs = append(a.tabs, &tab{id: id, screen: s})
	a.activeTab = len(a.tabs) - 1
	return tea.Batch(s.Init(), s.OnFocus())
}

func (a *App) openChat(msg screen.OpenChatMsg) tea.Cmd {
	if !msg.Fork && msg.Chat != nil {
		for i, t := range a.tabs {
			if t.id == msg.Chat.Name {
				return a.switchTab(i)
			}
		}
	}

	return func() tea.Msg {
		chat := msg.Chat
		var err error

		if msg.Fork && chat != nil {
			chat, err = a.store.ForkChat(a.ctx, chat)
			if err != nil {
				return screen.AlertMsg{Text: fmt.Sprintf("Fork failed: %v", err)}
			}
		}

		if chat == nil {
			chat, err = a.store.CreateChat(a.ctx, &sgptpb.Chat{
				Metadata: &sgptpb.ChatMetadata{
					CurrentModel: a.defaultParams.Model.Name,
				},
			})
			if err != nil {
				return screen.AlertMsg{Text: fmt.Sprintf("Create failed: %v", err)}
			}
		}

		params := a.defaultParams
		params.Chat = chat.Name
		tabID := chat.Name

		chatSession := session.New(a.ctx, a.store, a.registry, chat, params)
		chatScreen := screen.NewChatScreen(a.makeWrap(tabID), a.makeSend(tabID), chatSession, params.InjectedFiles)
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

func (a *App) focusMenuSearch() tea.Cmd {
	for i, t := range a.tabs {
		if t.id == menuTabID {
			cmd := a.switchTab(i)
			if menuModel, ok := t.screen.(*menuscreen.Model); ok {
				searchCmd := menuModel.ActivateSearch()
				return tea.Batch(cmd, searchCmd)
			}
			return cmd
		}
	}
	return nil
}

func (a *App) showAlert(text string) tea.Cmd {
	a.alertQueue = append(a.alertQueue, text)
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
	return tea.Tick(alertDuration, func(time.Time) tea.Msg { return alertDismissMsg{} })
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
	if a.height == 0 {
		return 0
	}
	return a.height - lipgloss.Height(a.renderTabBar()) - 1
}

func (a *App) renderTabBar() string {
	var tabs []widget.Tab
	for i, t := range a.tabs {
		streaming := false
		if chatScreen, ok := t.screen.(*screen.ChatScreen); ok {
			streaming = chatScreen.IsStreaming()
		}
		tabs = append(tabs, widget.Tab{
			ID:        t.id,
			Title:     t.screen.ShortTitle(),
			Active:    i == a.activeTab,
			Streaming: streaming,
		})
	}
	return widget.RenderTabBar(tabs, a.width)
}
