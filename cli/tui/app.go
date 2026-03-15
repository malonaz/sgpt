package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/google/uuid"
	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"google.golang.org/protobuf/proto"

	"github.com/malonaz/sgpt/cli/tui/component"
	"github.com/malonaz/sgpt/cli/tui/screen"
	chatscreen "github.com/malonaz/sgpt/cli/tui/screen/chat"
	menuscreen "github.com/malonaz/sgpt/cli/tui/screen/menu"
	searchscreen "github.com/malonaz/sgpt/cli/tui/screen/search"
	"github.com/malonaz/sgpt/cli/tui/styles"
	chatservicepb "github.com/malonaz/sgpt/genproto/chat/chat_service/v1"
	chatpb "github.com/malonaz/sgpt/genproto/chat/v1"
	"github.com/malonaz/sgpt/internal/configuration"
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
	keyQuit       = key.NewBinding(key.WithKeys("ctrl+d"))
	keyNewTab     = key.NewBinding(key.WithKeys("ctrl+t"))
	keyCloseTab   = key.NewBinding(key.WithKeys("ctrl+w"))
	keyNextTab    = key.NewBinding(key.WithKeys("alt+right", "alt+l"))
	keyPrevTab    = key.NewBinding(key.WithKeys("alt+left", "alt+h"))
	keyOpenMenu   = key.NewBinding(key.WithKeys("alt+m"))
	keyOpenSearch = key.NewBinding(key.WithKeys("ctrl+_"))
	keyTab1       = key.NewBinding(key.WithKeys("alt+f1"))
	keyTab2       = key.NewBinding(key.WithKeys("alt+f2"))
	keyTab3       = key.NewBinding(key.WithKeys("alt+f3"))
	keyTab4       = key.NewBinding(key.WithKeys("alt+f4"))
	keyTab5       = key.NewBinding(key.WithKeys("alt+f5"))
	keyTab6       = key.NewBinding(key.WithKeys("alt+f6"))
	keyTab7       = key.NewBinding(key.WithKeys("alt+f7"))
	keyTab8       = key.NewBinding(key.WithKeys("alt+f8"))
	keyTab9       = key.NewBinding(key.WithKeys("alt+f9"))
)

var tabIndexKeys = []key.Binding{keyTab1, keyTab2, keyTab3, keyTab4, keyTab5, keyTab6, keyTab7, keyTab8, keyTab9}

type App struct {
	ctx        context.Context
	config     *configuration.Config
	aiClient   aiservicepb.AiServiceClient
	chatClient chatservicepb.ChatServiceClient

	defaultChatOpts       chatscreen.Options
	defaultAdditionalMsgs []*aipb.Message
	defaultInjectedFiles  []string

	tabs      []*tab
	activeTab int

	program *tea.Program
	width   int
	height  int
	ready   bool

	alert        string
	alertVisible bool
	quitting     bool
}

func NewApp(
	ctx context.Context,
	config *configuration.Config,
	aiClient aiservicepb.AiServiceClient,
	chatClient chatservicepb.ChatServiceClient,
	initialChat *chatpb.Chat,
	chatOpts chatscreen.Options,
	additionalMessages []*aipb.Message,
	injectedFiles []string,
) *App {
	app := &App{
		ctx:                   ctx,
		config:                config,
		aiClient:              aiClient,
		chatClient:            chatClient,
		defaultChatOpts:       chatOpts,
		defaultAdditionalMsgs: additionalMessages,
		defaultInjectedFiles:  injectedFiles,
	}

	menuScreen := menuscreen.New(ctx, chatClient, app.makeWrap(menuTabID))

	tabID := chatOpts.ChatID
	chatScreen := chatscreen.New(
		ctx, config, aiClient, chatClient,
		app.makeWrap(tabID), app.makeSend(tabID),
		initialChat, chatOpts, additionalMessages, injectedFiles,
	)

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
		a.alertVisible = false
		return a, nil

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
		return a, a.openSearch()

	case screen.CloseTabMsg:
		return a, a.closeTab(msg.TabID)

	case tea.KeyPressMsg:
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

	var b strings.Builder
	b.WriteString(a.renderTabBar())
	b.WriteString("\n")
	if a.activeTab < len(a.tabs) {
		b.WriteString(a.tabs[a.activeTab].screen.View())
	}

	content := b.String()
	if a.alertVisible {
		alertStyle := lipgloss.NewStyle().
			Background(styles.SuccessColor).
			Foreground(lipgloss.Color("#000000")).
			Bold(true).
			Padding(0, 1)
		banner := alertStyle.Render(a.alert)
		content = lipgloss.JoinVertical(lipgloss.Left, banner, content)
	}

	view := tea.NewView(content)
	view.AltScreen = true
	view.ReportFocus = true
	return view
}

func (a *App) handleGlobalKey(msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case key.Matches(msg, keyQuit):
		a.quitting = true
		return tea.Quit
	case key.Matches(msg, keyNewTab):
		return a.createNewChat()
	case key.Matches(msg, keyCloseTab):
		return a.closeTab("")
	case key.Matches(msg, keyNextTab):
		return a.switchTab(a.activeTab + 1)
	case key.Matches(msg, keyPrevTab):
		return a.switchTab(a.activeTab - 1)
	case key.Matches(msg, keyOpenMenu):
		return a.focusMenu()
	case key.Matches(msg, keyOpenSearch):
		return a.openSearch()
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
			forked := proto.Clone(chat).(*chatpb.Chat)
			forked.Name = ""
			createChatRequest := &chatservicepb.CreateChatRequest{
				RequestId: uuid.New().String(),
				ChatId:    uuid.New().String()[:8],
				Chat:      forked,
			}
			chat, err = a.chatClient.CreateChat(a.ctx, createChatRequest)
			if err != nil {
				return screen.AlertMsg{Text: fmt.Sprintf("Fork failed: %v", err)}
			}
		}

		if chat == nil {
			createChatRequest := &chatservicepb.CreateChatRequest{
				RequestId: uuid.New().String(),
				ChatId:    uuid.New().String()[:8],
				Chat: &chatpb.Chat{
					Metadata: &chatpb.ChatMetadata{
						CurrentModel: a.defaultChatOpts.Model.Name,
					},
				},
			}
			chat, err = a.chatClient.CreateChat(a.ctx, createChatRequest)
			if err != nil {
				return screen.AlertMsg{Text: fmt.Sprintf("Create failed: %v", err)}
			}
		}

		opts := a.defaultChatOpts
		opts.ChatID = chat.Name
		tabID := chat.Name

		s := chatscreen.New(
			a.ctx, a.config, a.aiClient, a.chatClient,
			a.makeWrap(tabID), a.makeSend(tabID),
			chat, opts,
			a.defaultAdditionalMsgs, a.defaultInjectedFiles,
		)
		return openTabMsg{id: tabID, screen: s}
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

func (a *App) openSearch() tea.Cmd {
	for i, t := range a.tabs {
		if _, ok := t.screen.(*searchscreen.Model); ok {
			return a.switchTab(i)
		}
	}
	tabID := "search"
	s := searchscreen.New(a.ctx, a.chatClient, a.makeWrap(tabID))
	return a.addTab(tabID, s)
}

func (a *App) showAlert(text string) tea.Cmd {
	a.alert = text
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
	var tabs []component.Tab
	for i, t := range a.tabs {
		streaming := false
		if cs, ok := t.screen.(*chatscreen.Model); ok {
			streaming = cs.IsStreaming()
		}
		tabs = append(tabs, component.Tab{
			ID:        t.id,
			Title:     t.screen.ShortTitle(),
			Active:    i == a.activeTab,
			Streaming: streaming,
		})
	}
	return component.RenderTabBar(tabs, a.width)
}
