package webserver

import (
	"fmt"
	"net/http"
	"strings"

	chatservicepb "github.com/malonaz/sgpt/genproto/chat/chat_service/v1"
	chatpb "github.com/malonaz/sgpt/genproto/chat/v1"
)

func (s *Server) handleInbox(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	tags := r.URL.Query()["tag"]
	pageToken := r.URL.Query().Get("page_token")

	var chats []*chatpb.Chat
	var nextPageToken string

	if query != "" {
		resp, err := s.client.SearchChats(r.Context(), &chatservicepb.SearchChatsRequest{
			Query:     query,
			PageSize:  s.pageSize,
			PageToken: pageToken,
		})
		if err != nil {
			http.Error(w, "Failed to search chats", http.StatusInternalServerError)
			return
		}
		chats = resp.Chats
		nextPageToken = resp.NextPageToken
	} else {
		filter := buildTagFilter(tags)
		resp, err := s.client.ListChats(r.Context(), &chatservicepb.ListChatsRequest{
			PageSize:  s.pageSize,
			PageToken: pageToken,
			Filter:    filter,
			OrderBy:   "update_time desc",
		})
		if err != nil {
			http.Error(w, "Failed to list chats", http.StatusInternalServerError)
			return
		}
		chats = resp.Chats
		nextPageToken = resp.NextPageToken
	}

	chatViews := make([]ChatViewModel, 0, len(chats))
	for _, chat := range chats {
		chatViews = append(chatViews, ChatViewModel{
			Chat:          chat,
			ID:            chatIDFromName(chat.Name),
			FormattedTime: chat.UpdateTime.AsTime().Format("Jan 2, 2006 3:04 PM"),
		})
	}

	data := &PageData{
		Title:         "Inbox",
		Chats:         chatViews,
		Query:         query,
		ActiveTags:    tags,
		NextPageToken: nextPageToken,
	}

	if err := s.tmpl.ExecuteTemplate(w, "base", data); err != nil {
		http.Error(w, "Failed to render template", http.StatusInternalServerError)
	}
}

func buildTagFilter(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	var parts []string
	for _, tag := range tags {
		parts = append(parts, fmt.Sprintf(`"%s" in tags`, tag))
	}
	return strings.Join(parts, " AND ")
}
