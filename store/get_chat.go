package store

import (
	"database/sql"
	"fmt"
)

func (s *Store) GetChat(chatID string) (*Chat, error) {
	row := s.db.QueryRow(`
        SELECT id, title, creation_timestamp, update_timestamp, messages, files, favorite, tags
        FROM chats
        WHERE id = ?
    `, chatID)

	chat, err := scanChat(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("chat not found")
		}
		return nil, fmt.Errorf("querying chat: %w", err)
	}

	return chat, nil
}
