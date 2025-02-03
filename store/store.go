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
	// Time at which a chat was created.
	CreationTimestamp int64
	// time at which a chat was updated.
	UpdateTimestamp int64
	// The messages of this chat.
	Messages []*llm.Message
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
			creation_timestamp INTEGER NOT NULL,
			update_timestamp INTEGER NOT NULL,
			messages TEXT NOT NULL
		)
	`)
	if err != nil {
		return nil, errors.Wrap(err, "creating chats table")
	}

	return &Store{
		db: db,
	}, nil
}

// CreateChat instantiates and returns a new chat.
func (s *Store) CreateChat(id string) *Chat {
	now := time.Now().UnixMicro()
	return &Chat{
		ID:                id,
		CreationTimestamp: now,
		UpdateTimestamp:   now,
	}
}

// Write a chat to the store.
func (s *Store) Write(chat *Chat) error {
	chat.UpdateTimestamp = time.Now().UnixMicro()

	messages, err := json.Marshal(chat.Messages)
	if err != nil {
		return errors.Wrap(err, "marshaling messages")
	}

	// Use REPLACE INTO to handle both insert and update cases
	_, err = s.db.Exec(`
		REPLACE INTO chats (id, creation_timestamp, update_timestamp, messages)
		VALUES (?, ?, ?, ?)
	`, chat.ID, chat.CreationTimestamp, chat.UpdateTimestamp, string(messages))

	if err != nil {
		return errors.Wrap(err, "writing chat to database")
	}
	return nil
}

// Get a chat.
func (s *Store) Get(chatID string) (*Chat, error) {
	chat := &Chat{}
	var messagesJSON string

	err := s.db.QueryRow(`
		SELECT id, creation_timestamp, update_timestamp, messages
		FROM chats
		WHERE id = ?
	`, chatID).Scan(&chat.ID, &chat.CreationTimestamp, &chat.UpdateTimestamp, &messagesJSON)

	if err == sql.ErrNoRows {
		return nil, errors.New("chat does not exist")
	}
	if err != nil {
		return nil, errors.Wrap(err, "querying chat")
	}

	if err := json.Unmarshal([]byte(messagesJSON), &chat.Messages); err != nil {
		return nil, errors.Wrap(err, "unmarshaling messages")
	}

	return chat, nil
}

// List all the chats in the store.
func (s *Store) List(pageSize int) ([]*Chat, error) {
	rows, err := s.db.Query(`
		SELECT id, creation_timestamp, update_timestamp, messages
		FROM chats
		ORDER BY update_timestamp DESC
		LIMIT ?
	`, pageSize)
	if err != nil {
		return nil, errors.Wrap(err, "querying chats")
	}
	defer rows.Close()

	var chats []*Chat
	for rows.Next() {
		chat := &Chat{}
		var messagesJSON string
		if err := rows.Scan(&chat.ID, &chat.CreationTimestamp, &chat.UpdateTimestamp, &messagesJSON); err != nil {
			return nil, errors.Wrap(err, "scanning chat row")
		}
		if err := json.Unmarshal([]byte(messagesJSON), &chat.Messages); err != nil {
			return nil, errors.Wrap(err, "unmarshaling messages")
		}
		chats = append(chats, chat)
	}

	if err = rows.Err(); err != nil {
		return nil, errors.Wrap(err, "iterating chat rows")
	}

	return chats, nil
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}
