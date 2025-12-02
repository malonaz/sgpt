package store

import (
	"encoding/json"
	"fmt"
)

// CreateChatRequest represents a request to create a new chat
type CreateChatRequest struct {
	Chat *Chat
}

func (s *Store) CreateChat(req *CreateChatRequest) (*Chat, error) {
	if req.Chat == nil {
		return nil, fmt.Errorf("chat cannot be nil")
	}

	// Marshal messages to JSON
	messagesJSON, err := json.Marshal(req.Chat.Messages)
	if err != nil {
		return nil, fmt.Errorf("marshaling messages: %w", err)
	}

	// Marshal files to JSON, ensuring they're deduplicated and sorted
	filesJSON, err := json.Marshal(dedupeStringsSorted(req.Chat.Files))
	if err != nil {
		return nil, fmt.Errorf("marshaling files: %w", err)
	}

	// Marshal tags to JSON
	tagsJSON, err := json.Marshal(dedupeStringsSorted(req.Chat.Tags))
	if err != nil {
		return nil, fmt.Errorf("marshaling tags: %w", err)
	}

	// Compute searchable content for FTS
	textToIndex, err := s.computeSearchableContent(req.Chat)
	if err != nil {
		return nil, fmt.Errorf("computing searchable content: %w", err)
	}

	// Begin transaction
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	// Insert into main chats table
	_, err = tx.Exec(`
INSERT INTO chats (
    id,
    title,
    creation_timestamp,
    update_timestamp,
    messages,
    files,
    favorite,
    tags
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		req.Chat.ID,
		req.Chat.Title,
		req.Chat.CreationTimestamp,
		req.Chat.UpdateTimestamp,
		string(messagesJSON),
		string(filesJSON),
		boolToInt(req.Chat.Favorite),
		string(tagsJSON),
	)
	if err != nil {
		return nil, fmt.Errorf("inserting into chats table: %w", err)
	}

	// Insert into FTS table
	_, err = tx.Exec(`
		INSERT INTO chats_fts (
			id,
			searchable_content
		) VALUES (?, ?)`,
		req.Chat.ID,
		textToIndex,
	)
	if err != nil {
		return nil, fmt.Errorf("inserting into FTS table: %w", err)
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing transaction: %w", err)
	}

	return req.Chat, nil
}
