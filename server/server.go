package server

import (
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Masterminds/sprig/v3"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"

	"github.com/malonaz/sgpt/store"
)

//go:embed templates
var templatesFS embed.FS

type PageData struct {
	Title       string
	ShowBack    bool
	Chat        *ChatViewModel
	Chats       []ChatViewModel
	CurrentPage int
	TotalPages  int
}

// ChatViewModel represents a chat with formatted time for the template
type ChatViewModel struct {
	*store.Chat
	FormattedTime string
}

// NewServeCmd creates a new serve command
func NewServeCmd(s *store.Store) *cobra.Command {
	var opts struct {
		Port     int
		PageSize int
	}

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve a web interface for viewing chats",
		Long:  "Serve a web interface for viewing chats",
		RunE: func(cmd *cobra.Command, args []string) error {
			server := &Server{
				store:    s,
				pageSize: opts.PageSize,
			}
			return server.Start(opts.Port)
		},
	}

	cmd.Flags().IntVarP(&opts.Port, "port", "p", 8080, "Port to serve on")
	cmd.Flags().IntVar(&opts.PageSize, "page-size", 50, "Number of chats to display")
	return cmd
}

// Server handles the web interface
type Server struct {
	store    *store.Store
	pageSize int
	tmpl     *template.Template
}

func (s *Server) Start(port int) error {
	funcMap := sprig.HtmlFuncMap()
	funcMap["formatMessage"] = formatMessage

	tmpl, err := template.New("").Funcs(funcMap).ParseFS(templatesFS,
		"templates/*.tmpl",
		"templates/includes/*.tmpl",
		"templates/pages/*.tmpl",
	)
	if err != nil {
		return errors.Wrap(err, "parsing template")
	}
	s.tmpl = tmpl

	http.HandleFunc("/", s.handleInbox)
	http.HandleFunc("/chat/", s.handleChat)

	addr := fmt.Sprintf(":%d", port)
	fmt.Printf("Server starting on http://localhost%s\n", addr)
	return http.ListenAndServe(addr, nil)
}

func (s *Server) handleInbox(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	page := 1
	if pageStr := r.URL.Query().Get("page"); pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}

	resp, err := s.store.ListChats(store.ListChatsRequest{
		Page:     page,
		PageSize: s.pageSize,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	viewModels := make([]ChatViewModel, len(resp.Chats))
	for i, chat := range resp.Chats {
		viewModels[i] = ChatViewModel{
			Chat:          chat,
			FormattedTime: time.UnixMicro(chat.UpdateTimestamp).Format(time.RFC822),
		}
	}

	data := PageData{
		Title:       "Inbox",
		ShowBack:    false,
		Chats:       viewModels,
		CurrentPage: page,
		TotalPages:  resp.PageCount,
	}

	if err := s.tmpl.ExecuteTemplate(w, "base", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) != 3 {
		http.NotFound(w, r)
		return
	}

	chatID := parts[2]
	chat, err := s.store.GetChat(chatID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	viewModel := ChatViewModel{
		Chat:          chat,
		FormattedTime: time.UnixMicro(chat.UpdateTimestamp).Format(time.RFC822),
	}

	data := PageData{
		Title:    fmt.Sprintf("Chat %s", chatID),
		ShowBack: true,
		Chat:     &viewModel,
	}

	if err := s.tmpl.ExecuteTemplate(w, "base", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
