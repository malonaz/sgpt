package store

import "fmt"

// DeleteChat removes a chat and its associated FTS entry from the database
func (s *Store) DeleteChat(chatID string) error {
	// Begin transaction
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	// Delete from main chats table
	result, err := tx.Exec(`DELETE FROM chats WHERE id = ?`, chatID)
	if err != nil {
		return fmt.Errorf("deleting chat from database: %w", err)
	}

	// Check if the chat existed
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("chat not found")
	}

	// Delete from FTS table
	_, err = tx.Exec(`DELETE FROM chats_fts WHERE id = ?`, chatID)
	if err != nil {
		return fmt.Errorf("deleting from FTS table: %w", err)
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing transaction: %w", err)
	}

	return nil
}
