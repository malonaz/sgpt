package store

import (
	"fmt"
)

// SearchChatsRequest contains parameters for searching chats
type SearchChatsRequest struct {
	Query    string
	Page     int
	PageSize int
}

// SearchChatsResponse contains the result of a search operation
type SearchChatsResponse struct {
	Chats      []*Chat
	TotalCount int
	PageCount  int
}

func (s *Store) SearchChats(req SearchChatsRequest) (*SearchChatsResponse, error) {
	if req.Query == "" {
		return &SearchChatsResponse{
			Chats:      []*Chat{},
			TotalCount: 0,
			PageCount:  0,
		}, nil
	}

	var total int
	err := s.db.QueryRow(`
        SELECT COUNT(*)
        FROM chats_fts
        WHERE chats_fts MATCH ?
    `, req.Query).Scan(&total)
	if err != nil {
		return nil, fmt.Errorf("counting search results: %%w", err)
	}

	pageCount := (total + req.PageSize - 1) / req.PageSize
	offset := (req.Page - 1) * req.PageSize

	rows, err := s.db.Query(`
        SELECT c.id, c.title, c.creation_timestamp, c.update_timestamp, c.messages, c.files, c.favorite, c.tags
        FROM chats c
        JOIN chats_fts fts ON c.id = fts.id
        WHERE fts.searchable_content MATCH ?
        ORDER BY c.update_timestamp DESC
        LIMIT ? OFFSET ?
    `, req.Query, req.PageSize, offset)
	if err != nil {
		return nil, fmt.Errorf("querying search results: %%w", err)
	}
	defer rows.Close()

	chats, err := scanChats(rows)
	if err != nil {
		return nil, err
	}

	return &SearchChatsResponse{
		Chats:      chats,
		TotalCount: total,
		PageCount:  pageCount,
	}, nil
}
