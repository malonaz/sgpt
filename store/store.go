package store

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/pkg/errors"
	_ "modernc.org/sqlite"

	"github.com/malonaz/sgpt/internal/llm"
)

// Chat represents a holds a chat.
type Chat struct {
	// ID of this chat.
	ID string
	// Title of the chat
	Title *string
	// Time at which a chat was created.
	CreationTimestamp int64
	// time at which a chat was updated.
	UpdateTimestamp int64
	// The messages of this chat.
	Messages []*llm.Message
	// Files we are using in this chat.
	Files []string
}

// Store implements a SQLite store for chats.
type Store struct {
	db *sql.DB
}

// New store.
func New(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, errors.Wrap(err, "opening database")
	}

	// Create chats table if it doesn't exist
	_, err = db.Exec(`
        CREATE TABLE IF NOT EXISTS chats (
            id TEXT PRIMARY KEY,
            title TEXT,
            creation_timestamp INTEGER NOT NULL,
            update_timestamp INTEGER NOT NULL,
            messages TEXT NOT NULL,
            files TEXT NOT NULL DEFAULT '[]'
        )
	`)
	if err != nil {
		return nil, errors.Wrap(err, "creating chats table")
	}

	return &Store{
		db: db,
	}, nil
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// CreateChat instantiates and returns a new chat.
func (s *Store) CreateChat(id string, files []string) *Chat {
	now := time.Now().UnixMicro()
	return &Chat{
		ID:                id,
		CreationTimestamp: now,
		UpdateTimestamp:   now,
		Files:             files,
	}
}

func (s *Store) UpdateChat(chat *Chat) error {
	chat.UpdateTimestamp = time.Now().UnixMicro()

	messages, err := json.Marshal(chat.Messages)
	if err != nil {
		return errors.Wrap(err, "marshaling messages")
	}

	files, err := json.Marshal(dedupeStringsSorted(chat.Files))
	if err != nil {
		return errors.Wrap(err, "marshaling files")
	}

	_, err = s.db.Exec(`
        REPLACE INTO chats (id, title, creation_timestamp, update_timestamp, messages, files)
        VALUES (?, ?, ?, ?, ?, ?)
    `, chat.ID, chat.Title, chat.CreationTimestamp, chat.UpdateTimestamp, string(messages), string(files))

	if err != nil {
		return errors.Wrap(err, "writing chat to database")
	}
	return nil
}

// ListChatsRequest contains parameters for listing chats
type ListChatsRequest struct {
	Page     int
	PageSize int
}

// ListChatsResponse contains the result of a list chats operation
type ListChatsResponse struct {
	Chats      []*Chat
	TotalCount int
	PageCount  int
}

// ListChats returns a paginated list of chats
func (s *Store) ListChats(req ListChatsRequest) (*ListChatsResponse, error) {
	// Get total count
	var total int
	err := s.db.QueryRow("SELECT COUNT(*) FROM chats").Scan(&total)
	if err != nil {
		return nil, errors.Wrap(err, "counting chats")
	}

	// Calculate pagination
	pageCount := (total + req.PageSize - 1) / req.PageSize
	offset := (req.Page - 1) * req.PageSize

	rows, err := s.db.Query(`
    SELECT id, title, creation_timestamp, update_timestamp, messages, files
    FROM chats
    ORDER BY update_timestamp DESC
    LIMIT ? OFFSET ?
`, req.PageSize, offset)
	if err != nil {
		return nil, errors.Wrap(err, "querying chats")
	}
	defer rows.Close()

	var chats []*Chat
	for rows.Next() {
		chat := &Chat{}
		var messagesJSON, filesJSON string
		if err := rows.Scan(&chat.ID, &chat.Title, &chat.CreationTimestamp, &chat.UpdateTimestamp, &messagesJSON, &filesJSON); err != nil {
			return nil, errors.Wrap(err, "scanning chat row")
		}
		if err := json.Unmarshal([]byte(messagesJSON), &chat.Messages); err != nil {
			return nil, errors.Wrap(err, "unmarshaling messages")
		}
		if err := json.Unmarshal([]byte(filesJSON), &chat.Files); err != nil {
			return nil, errors.Wrap(err, "unmarshaling files")
		}
		chats = append(chats, chat)
	}

	if err = rows.Err(); err != nil {
		return nil, errors.Wrap(err, "iterating chat rows")
	}

	return &ListChatsResponse{
		Chats:      chats,
		TotalCount: total,
		PageCount:  pageCount,
	}, nil
}

func (s *Store) GetChat(chatID string) (*Chat, error) {
	chat := &Chat{}
	var messagesJSON, filesJSON string

	err := s.db.QueryRow(`
        SELECT id, title, creation_timestamp, update_timestamp, messages, files
        FROM chats
        WHERE id = ?
    `, chatID).Scan(&chat.ID, &chat.Title, &chat.CreationTimestamp, &chat.UpdateTimestamp, &messagesJSON, &filesJSON)

	if err == sql.ErrNoRows {
		return nil, errors.New("chat not found")
	}
	if err != nil {
		return nil, errors.Wrap(err, "querying chat")
	}

	if err := json.Unmarshal([]byte(messagesJSON), &chat.Messages); err != nil {
		return nil, errors.Wrap(err, "unmarshaling messages")
	}
	if err := json.Unmarshal([]byte(filesJSON), &chat.Files); err != nil {
		return nil, errors.Wrap(err, "unmarshaling files")
	}

	return chat, nil
}

func (s *Store) UpdateChatTitle(chatID string, title string) error {
	result, err := s.db.Exec(`
        UPDATE chats
        SET title = ?,
            update_timestamp = ?
        WHERE id = ?
    `, &title, time.Now().UnixMicro(), chatID)

	if err != nil {
		return errors.Wrap(err, "updating chat title")
	}

	// Check if the chat exists
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return errors.Wrap(err, "checking rows affected")
	}
	if rowsAffected == 0 {
		return errors.New("chat not found")
	}

	return nil
}
