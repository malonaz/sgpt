package webserver

import (
	"net/http"

	chatservicepb "github.com/malonaz/sgpt/genproto/chat/chat_service/v1"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

func (s *Server) handleRemoveTag(w http.ResponseWriter, r *http.Request, chatID, tagToRemove string) {
	chat, err := s.client.GetChat(r.Context(), &chatservicepb.GetChatRequest{
		Name: chatName(chatID),
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	newTags := make([]string, 0, len(chat.Tags))
	for _, tag := range chat.Tags {
		if tag != tagToRemove {
			newTags = append(newTags, tag)
		}
	}
	chat.Tags = newTags

	_, err = s.client.UpdateChat(r.Context(), &chatservicepb.UpdateChatRequest{
		Chat:       chat,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"tags"}},
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}
