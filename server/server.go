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
		return errors.Wrap(err, "parsing template")
	}
	s.tmpl = tmpl

	http.HandleFunc("/", s.handleInbox)
	http.HandleFunc("/chat/", s.handleChatRoutes)

	addr := fmt.Sprintf(":%d", port)
	fmt.Printf("Server starting on http://localhost%s\n", addr)
	return http.ListenAndServe(addr, nil)
}

func (s *Server) handleInbox(w http.ResponseWriter, r *http.Request) {
	// Get page number from query parameters
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 1 {
		page = 1
	}

	// Get search query from query parameters
	query := r.URL.Query().Get("q")

	// Get tags from query parameters
	tags := r.URL.Query()["tag"]

	// Define page size
	const pageSize = 10

	var chats []*store.Chat
	var totalPages int

	// Handle either search or regular listing based on query presence
	if query != "" {
		// Handle search
		searchResp, err := s.store.SearchChats(store.SearchChatsRequest{
			Query:    query,
			Page:     page,
			PageSize: pageSize,
		})
		if err != nil {
			http.Error(w, "Failed to search chats", http.StatusInternalServerError)
			return
		}
		chats = searchResp.Chats
		totalPages = searchResp.PageCount
	} else {
		// Handle regular listing with optional tag filtering
		listResp, err := s.store.ListChats(&store.ListChatsRequest{
			Page:     page,
			PageSize: pageSize,
			Tags:     tags,
		})
		if err != nil {
			http.Error(w, "Failed to list chats", http.StatusInternalServerError)
			return
		}
		chats = listResp.Chats
		totalPages = listResp.PageCount
	}

	chatViews := []ChatViewModel{}
	// Format timestamps for each chat
	for _, chat := range chats {
		chatViews = append(chatViews, ChatViewModel{
			Chat:          chat,
			FormattedTime: time.UnixMicro(chat.UpdateTimestamp).Format("Jan 2, 2006 3:04 PM"),
		})
	}

	// Prepare template data using PageData
	data := &PageData{
		Title:       "Inbox",
		Chats:       chatViews,
		CurrentPage: page,
		TotalPages:  totalPages,
		Query:       query,
		ActiveTags:  tags,
	}

	// Execute template
	if err := s.tmpl.ExecuteTemplate(w, "base", data); err != nil {
		http.Error(w, "Failed to render template", http.StatusInternalServerError)
		return
	}
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
	case r.Method == "DELETE" && len(parts) == 5 && parts[3] == "tags":
		s.handleRemoveTag(w, r, chatID, parts[4])
	default:
		http.NotFound(w, r)
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

func (s *Server) handleAddTag(w http.ResponseWriter, r *http.Request, chatID string) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	tag := r.FormValue("tag")
	if tag == "" {
		http.Error(w, "Tag cannot be empty", http.StatusBadRequest)
		return
	}

	// Get existing chat
	chat, err := s.store.GetChat(chatID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Add new tag
	chat.Tags = append(chat.Tags, tag)

	// Update chat
	err = s.store.UpdateChat(&store.UpdateChatRequest{
		Chat:       chat,
		UpdateMask: []string{"tags"},
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Redirect back to chat page
	http.Redirect(w, r, "/chat/"+chatID, http.StatusSeeOther)
}

func (s *Server) handleRemoveTag(w http.ResponseWriter, r *http.Request, chatID, tagToRemove string) {
	// Get existing chat
	chat, err := s.store.GetChat(chatID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Remove the tag
	newTags := make([]string, 0, len(chat.Tags))
	for _, tag := range chat.Tags {
		if tag != tagToRemove {
			newTags = append(newTags, tag)
		}
	}
	chat.Tags = newTags

	// Update chat
	err = s.store.UpdateChat(&store.UpdateChatRequest{
		Chat:       chat,
		UpdateMask: []string{"tags"},
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}
