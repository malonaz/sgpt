package server

import (
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"github.com/Masterminds/sprig/v3"
	"github.com/spf13/cobra"

	"github.com/malonaz/sgpt/store"
)

//go:embed templates
var templatesFS embed.FS

type PageData struct {
	Title       string
	Query       string
	ShowBack    bool
	Chat        *ChatViewModel
	Chats       []ChatViewModel
	CurrentPage int
	TotalPages  int
	ActiveTags  []string
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

	cmd.Flags().IntVarP(&opts.Port, "port", "p", 3030, "Port to serve on")
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
		return fmt.Errorf("parsing template: %w", err)
	}
	s.tmpl = tmpl

	http.HandleFunc("/", s.handleInbox)
	http.HandleFunc("/chat/", s.handleChatRoutes)

	addr := fmt.Sprintf(":%d", port)
	fmt.Printf("Server starting on http://localhost%s\n", addr)
	return http.ListenAndServe(addr, nil)
}
func (s *Server) handleChatRoutes(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 3 {
		http.NotFound(w, r)
		return
	}

	chatID := parts[2]

	// Handle different routes based on the path and method
	switch {
	case r.Method == "GET" && len(parts) == 3:
		s.handleChat(w, r)
	case r.Method == "POST" && len(parts) == 4 && parts[3] == "tags":
		s.handleAddTag(w, r, chatID)
	case r.Method == "DELETE" && len(parts) == 3:
		s.handleDeleteChat(w, r, chatID)
	case r.Method == "DELETE" && len(parts) == 5 && parts[3] == "tags":
		s.handleRemoveTag(w, r, chatID, parts[4])
	default:
		http.NotFound(w, r)
	}
}
