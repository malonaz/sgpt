package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
)

func boolToInt(val bool) int {
	if val {
		return 1
	}
	return 0
}

func dedupeStringsSorted(strings []string) []string {
	if len(strings) == 0 {
		return strings
	}

	// Create a copy
	copied := make([]string, len(strings))
	copy(copied, strings)

	// Sort the copy
	sort.Strings(copied)

	// Remove duplicates
	j := 0
	for i := 1; i < len(copied); i++ {
		if copied[j] != copied[i] {
			j++
			copied[j] = copied[i]
		}
	}

	return copied[:j+1]
}

func scanChat(row interface{ Scan(...interface{}) error }) (*Chat, error) {
	chat := &Chat{}
	var messagesJSON, filesJSON, tagsJSON string
	var favorite int

	if err := row.Scan(&chat.ID, &chat.Title, &chat.CreationTimestamp,
		&chat.UpdateTimestamp, &messagesJSON, &filesJSON, &favorite, &tagsJSON); err != nil {
		return nil, fmt.Errorf("scanning chat row: %w", err)
	}

	if err := json.Unmarshal([]byte(messagesJSON), &chat.Messages); err != nil {
		return nil, fmt.Errorf("unmarshaling messages: %w", err)
	}
	if err := json.Unmarshal([]byte(filesJSON), &chat.Files); err != nil {
		return nil, fmt.Errorf("unmarshaling files: %w", err)
	}
	if err := json.Unmarshal([]byte(tagsJSON), &chat.Tags); err != nil {
		return nil, fmt.Errorf("unmarshaling tags: %w", err)
	}

	chat.Favorite = favorite != 0

	return chat, nil
}

// scanChats helps avoid duplicate chat scanning code
func scanChats(rows *sql.Rows) ([]*Chat, error) {
	var chats []*Chat
	for rows.Next() {
		chat, err := scanChat(rows)
		if err != nil {
			return nil, err
		}
		chats = append(chats, chat)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating chat rows: %w", err)
	}
	return chats, nil
}
