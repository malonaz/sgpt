package webserver

import (
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"github.com/Masterminds/sprig/v3"
	"github.com/spf13/cobra"

	chatservicepb "github.com/malonaz/sgpt/genproto/chat/chat_service/v1"
	chatpb "github.com/malonaz/sgpt/genproto/chat/v1"
)

//go:embed templates
var templatesFS embed.FS

type PageData struct {
	Title         string
	Query         string
	Chat          *ChatViewModel
	Chats         []ChatViewModel
	CurrentPage   int
	TotalPages    int
	ActiveTags    []string
	NextPageToken string
	PrevTokens    []string
}

type ChatViewModel struct {
	*chatpb.Chat
	ID            string
	FormattedTime string
}

func NewServeCmd(chatClient chatservicepb.ChatServiceClient) *cobra.Command {
	var opts struct {
		Port     int
		PageSize int
	}

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve a web interface for viewing chats",
		RunE: func(cmd *cobra.Command, args []string) error {
			server := &Server{
				client:   chatClient,
				pageSize: int32(opts.PageSize),
			}
			return server.Start(opts.Port)
		},
	}

	cmd.Flags().IntVarP(&opts.Port, "port", "p", 3030, "Port to serve on")
	cmd.Flags().IntVar(&opts.PageSize, "page-size", 50, "Number of chats to display")
	return cmd
}

type Server struct {
	client   chatservicepb.ChatServiceClient
	pageSize int32
	tmpl     *template.Template
}

func (s *Server) Start(port int) error {
	funcMap := sprig.HtmlFuncMap()
	funcMap["formatMessage"] = formatMessage
	funcMap["messageRole"] = messageRole

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

func chatIDFromName(name string) string {
	parts := strings.Split(name, "/")
	if len(parts) >= 2 {
		return parts[len(parts)-1]
	}
	return name
}

func chatName(id string) string {
	if strings.HasPrefix(id, "chats/") {
		return id
	}
	return "chats/" + id
}
