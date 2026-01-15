package webserver

import (
	"net/http"
	"strings"

	chatservicepb "github.com/malonaz/sgpt/genproto/chat/chat_service/v1"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) != 3 {
		http.NotFound(w, r)
		return
	}

	chatID := parts[2]
	chat, err := s.client.GetChat(r.Context(), &chatservicepb.GetChatRequest{
		Name: chatName(chatID),
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	viewModel := ChatViewModel{
		Chat:          chat,
		ID:            chatIDFromName(chat.Name),
		FormattedTime: chat.UpdateTime.AsTime().Format("Jan 2, 2006 3:04 PM"),
	}

	chatTitle := "Unnamed chat"
	if chat.Metadata != nil && chat.Metadata.Title != "" {
		chatTitle = chat.Metadata.Title
	}

	data := PageData{
		Title: chatTitle,
		Chat:  &viewModel,
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

	chat, err := s.client.GetChat(r.Context(), &chatservicepb.GetChatRequest{
		Name: chatName(chatID),
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	chat.Tags = append(chat.Tags, tag)

	_, err = s.client.UpdateChat(r.Context(), &chatservicepb.UpdateChatRequest{
		Chat:       chat,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"tags"}},
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/chat/"+chatID, http.StatusSeeOther)
}

func (s *Server) handleDeleteChat(w http.ResponseWriter, r *http.Request, chatID string) {
	_, err := s.client.DeleteChat(r.Context(), &chatservicepb.DeleteChatRequest{
		Name: chatName(chatID),
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if r.Header.Get("X-Requested-With") == "XMLHttpRequest" {
		w.WriteHeader(http.StatusOK)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}
