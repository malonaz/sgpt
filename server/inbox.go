package server

import (
	"net/http"
	"strconv"
	"time"

	"github.com/malonaz/sgpt/store"
)

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
