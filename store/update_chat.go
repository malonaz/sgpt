package store

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// UpdateChatRequest represents a request to update a chat with specific fields
type UpdateChatRequest struct {
	Chat       *Chat
	UpdateMask []string
}

func (s *Store) UpdateChat(req *UpdateChatRequest) error {
	if req.Chat == nil {
		return fmt.Errorf("chat cannot be nil")
	}

	// Get the existing chat to preserve non-updated fields
	existingChat, err := s.GetChat(req.Chat.ID)
	if err != nil {
		return fmt.Errorf("retrieving existing chat: %w", err)
	}

	// Always update the timestamp
	req.Chat.UpdateTimestamp = time.Now().UnixMicro()

	// Prepare the update query parts
	var setClauses []string
	var args []interface{}

	// Helper to check if a field should be updated
	shouldUpdate := func(field string) bool {
		for _, f := range req.UpdateMask {
			if f == field {
				return true
			}
		}
		return false
	}

	// Build the update query based on UpdateMask
	if shouldUpdate("title") {
		setClauses = append(setClauses, "title = ?")
		args = append(args, req.Chat.Title)
		existingChat.Title = req.Chat.Title
	}

	if shouldUpdate("messages") {
		messagesJSON, err := json.Marshal(req.Chat.Messages)
		if err != nil {
			return fmt.Errorf("marshaling messages: %w", err)
		}
		setClauses = append(setClauses, "messages = ?")
		args = append(args, string(messagesJSON))
		existingChat.Messages = req.Chat.Messages
	}

	if shouldUpdate("files") {
		filesJSON, err := json.Marshal(dedupeStringsSorted(req.Chat.Files))
		if err != nil {
			return fmt.Errorf("marshaling files: %w", err)
		}
		setClauses = append(setClauses, "files = ?")
		args = append(args, string(filesJSON))
		existingChat.Files = req.Chat.Files
	}

	if shouldUpdate("tags") {
		tagsJSON, err := json.Marshal(dedupeStringsSorted(req.Chat.Tags))
		if err != nil {
			return fmt.Errorf("marshaling tags: %w", err)
		}
		setClauses = append(setClauses, "tags = ?")
		args = append(args, string(tagsJSON))
		existingChat.Tags = req.Chat.Tags
	}

	if shouldUpdate("favorite") {
		setClauses = append(setClauses, "favorite = ?")
		args = append(args, boolToInt(req.Chat.Favorite))
		existingChat.Favorite = req.Chat.Favorite
	}

	// Always update the timestamp
	setClauses = append(setClauses, "update_timestamp = ?")
	args = append(args, req.Chat.UpdateTimestamp)
	existingChat.UpdateTimestamp = req.Chat.UpdateTimestamp

	// If nothing to update, return early
	if len(setClauses) == 1 { // Only timestamp
		return nil
	}

	// Add the WHERE clause argument
	args = append(args, req.Chat.ID)

	// Compute new searchable content
	textToIndex, err := s.computeSearchableContent(existingChat)
	if err != nil {
		return fmt.Errorf("computing searchable content: %w", err)
	}

	// Begin transaction
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	// Update the chats table
	query := fmt.Sprintf("UPDATE chats SET %s WHERE id = ?", strings.Join(setClauses, ", "))
	_, err = tx.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("updating chat in database: %w", err)
	}

	// Update the FTS table
	_, err = tx.Exec(`DELETE FROM chats_fts WHERE id = ?`, req.Chat.ID)
	if err != nil {
		return fmt.Errorf("deleting from FTS table: %w", err)
	}

	_, err = tx.Exec(`INSERT INTO chats_fts (id, searchable_content) VALUES (?, ?)`,
		req.Chat.ID, textToIndex)
	if err != nil {
		return fmt.Errorf("inserting into FTS table: %w", err)
	}

	return tx.Commit()
}
