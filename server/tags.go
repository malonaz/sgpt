package server

import (
	"net/http"

	"github.com/malonaz/sgpt/store"
)

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
