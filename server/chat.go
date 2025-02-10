package server

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/malonaz/sgpt/store"
)

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

func (s *Server) handleDeleteChat(w http.ResponseWriter, r *http.Request, chatID string) {
	if err := s.store.DeleteChat(chatID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// If the request is AJAX, return 200 OK
	if r.Header.Get("X-Requested-With") == "XMLHttpRequest" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Otherwise redirect to inbox
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
