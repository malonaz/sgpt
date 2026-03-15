package search

import (
	"context"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/malonaz/sgpt/cli/tui/screen"
	chatservicepb "github.com/malonaz/sgpt/genproto/chat/chat_service/v1"
	chatpb "github.com/malonaz/sgpt/genproto/chat/v1"
)

const debounceInterval = 300 * time.Millisecond

type searchResultsMsg struct {
	Chats []*chatpb.Chat
	Err   error
	Query string
}

type debounceTickMsg struct {
	Query string
}

type Model struct {
	ctx        context.Context
	chatClient chatservicepb.ChatServiceClient
	wrap       screen.WrapFunc

	queryInput textarea.Model
	results    []*chatpb.Chat
	cursor     int
	loading    bool
	err        error
	lastQuery  string

	viewport viewport.Model
	width    int
	height   int
	ready    bool
	focused  bool
}

func New(ctx context.Context, chatClient chatservicepb.ChatServiceClient, wrap screen.WrapFunc) *Model {
	queryInput := textarea.New()
	queryInput.Placeholder = "Search chats..."
	queryInput.CharLimit = 256
	queryInput.SetHeight(1)
	queryInput.ShowLineNumbers = false
	queryInput.Prompt = "🔍 "
	queryInput.Focus()

	return &Model{
		ctx:        ctx,
		chatClient: chatClient,
		wrap:       wrap,
		queryInput: queryInput,
	}
}

func (m *Model) Init() tea.Cmd {
	return textarea.Blink
}

func (m *Model) Title() string      { return "Search" }
func (m *Model) ShortTitle() string { return "Search" }

func (m *Model) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.recalculateLayout()
}

func (m *Model) OnFocus() tea.Cmd {
	m.focused = true
	m.queryInput.Focus()
	return textarea.Blink
}

func (m *Model) OnBlur() {
	m.focused = false
	m.queryInput.Blur()
}

func (m *Model) executeSearch(query string) tea.Cmd {
	m.loading = true
	wrap := m.wrap
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 10*time.Second)
		defer cancel()

		searchChatsRequest := &chatservicepb.SearchChatsRequest{
			Query:    query,
			PageSize: 50,
		}
		searchChatsResponse, err := m.chatClient.SearchChats(ctx, searchChatsRequest)
		if err != nil {
			return wrap(searchResultsMsg{Err: err, Query: query})
		}
		return wrap(searchResultsMsg{Chats: searchChatsResponse.Chats, Query: query})
	}
}

func (m *Model) recalculateLayout() {
	if m.width == 0 || m.height == 0 {
		return
	}

	viewportHeight := m.height - 5
	if viewportHeight < 1 {
		viewportHeight = 1
	}

	if !m.ready {
		m.viewport = viewport.New(
			viewport.WithWidth(m.width),
			viewport.WithHeight(viewportHeight),
		)
		m.ready = true
	} else {
		m.viewport.SetWidth(m.width)
		m.viewport.SetHeight(viewportHeight)
	}

	m.queryInput.SetWidth(m.width - 6)
}

var _ screen.Screen = (*Model)(nil)
