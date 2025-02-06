package store

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ListChatsRequest contains parameters for listing chats
type ListChatsRequest struct {
	Filter   string
	Page     int
	PageSize int
	Tags     []string
}

// ListChatsResponse contains the result of a list chats operation
type ListChatsResponse struct {
	Chats      []*Chat
	TotalCount int
	PageCount  int
}

func (s *Store) ListChats(req *ListChatsRequest) (*ListChatsResponse, error) {
	if req.PageSize == 0 {
		req.PageSize = 500 // Default.
	}

	// Build the WHERE clause
	whereClause := strings.Builder{}
	var args []interface{}

	if req.Filter != "" {
		whereClause.WriteString(req.Filter)
	}

	if len(req.Tags) > 0 {
		if whereClause.Len() > 0 {
			whereClause.WriteString(" AND ")
		}
		tagsJSON, err := json.Marshal(dedupeStringsSorted(req.Tags))
		if err != nil {
			return nil, fmt.Errorf("marshaling tags filter: %w", err)
		}
		// Use JSON_EACH to ensure ALL requested tags are present
		whereClause.WriteString(`
        (
            SELECT COUNT(DISTINCT value)
            FROM json_each(?)
        ) = (
            SELECT COUNT(*)
            FROM json_each(?)
            WHERE value IN (
                SELECT value FROM json_each(tags)
            )
        )
    `)
		args = append(args, string(tagsJSON), string(tagsJSON))
	}

	// Get total count
	countQuery := "SELECT COUNT(*) FROM chats"
	if whereClause.Len() > 0 {
		countQuery += " WHERE " + whereClause.String()
	}

	var total int
	err := s.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		return nil, fmt.Errorf("counting chats: %w", err)
	}

	pageCount := (total + req.PageSize - 1) / req.PageSize
	offset := (req.Page - 1) * req.PageSize

	// Build the final query
	query := `
        SELECT id, title, creation_timestamp, update_timestamp, messages, files, favorite, tags
        FROM chats
    `
	if whereClause.Len() > 0 {
		query += " WHERE " + whereClause.String()
	}
	query += ` ORDER BY update_timestamp DESC LIMIT ? OFFSET ?`

	// Add pagination parameters to args
	args = append(args, req.PageSize, offset)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying chats: %w", err)
	}
	defer rows.Close()

	chats, err := scanChats(rows)
	if err != nil {
		return nil, err
	}

	return &ListChatsResponse{
		Chats:      chats,
		TotalCount: total,
		PageCount:  pageCount,
	}, nil
}
