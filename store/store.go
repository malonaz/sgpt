package store

import (
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/pkg/errors"
	_ "modernc.org/sqlite"

	"github.com/malonaz/sgpt/internal/llm"
)

// Chat represents a chat.
type Chat struct {
	ID                string
	Title             *string
	CreationTimestamp int64
	UpdateTimestamp   int64
	Messages          []*llm.Message
	Files             []string
}

// Store implements a SQLite store for chats.
type Store struct {
	db *sql.DB
}

// New initializes a new Store with the specified SQLite database path.
func New(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, errors.Wrap(err, "opening database")
	}

	// Create the main chats table if it doesn't exist
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

	// Create the FTS table
	_, err = db.Exec(`
        CREATE VIRTUAL TABLE IF NOT EXISTS chats_fts USING fts5(
            id UNINDEXED,
            searchable_content
        )
    `)
	if err != nil {
		return nil, errors.Wrap(err, "creating FTS5 table")
	}

	return &Store{
		db: db,
	}, nil
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// CreateChat initializes and returns a new chat.
func (s *Store) CreateChat(id string, files []string) *Chat {
	now := time.Now().UnixMicro()
	return &Chat{
		ID:                id,
		CreationTimestamp: now,
		UpdateTimestamp:   now,
		Files:             files,
	}
}

// computeSearchableContent concatenates the chat title and all message contents into a single searchable string.
func (s *Store) computeSearchableContent(chat *Chat) (string, error) {
	var contentParts []string

	// Include the title if it exists
	if chat.Title != nil {
		contentParts = append(contentParts, *chat.Title)
	}

	// Include the content of all messages
	for _, message := range chat.Messages {
		contentParts = append(contentParts, message.Content)
	}

	// Combine everything into one string
	return strings.Join(contentParts, " "), nil
}

func (s *Store) UpdateChat(chat *Chat) error {
	chat.UpdateTimestamp = time.Now().UnixMicro()

	// Serialize messages and files as JSON
	messagesJSON, err := json.Marshal(chat.Messages)
	if err != nil {
		return errors.Wrap(err, "marshaling messages")
	}
	filesJSON, err := json.Marshal(dedupeStringsSorted(chat.Files))
	if err != nil {
		return errors.Wrap(err, "marshaling files")
	}

	// Compute the searchable content for the FTS table
	textToIndex, err := s.computeSearchableContent(chat)
	if err != nil {
		return errors.Wrap(err, "computing searchable content")
	}

	// Start a transaction
	tx, err := s.db.Begin()
	if err != nil {
		return errors.Wrap(err, "beginning transaction")
	}
	defer tx.Rollback()

	// Update the main chats table
	_, err = tx.Exec(`
        REPLACE INTO chats (id, title, creation_timestamp, update_timestamp, messages, files)
        VALUES (?, ?, ?, ?, ?, ?)
    `, chat.ID, chat.Title, chat.CreationTimestamp, chat.UpdateTimestamp, string(messagesJSON), string(filesJSON))
	if err != nil {
		return errors.Wrap(err, "writing chat to database")
	}

	// Update the FTS table using delete and insert
	_, err = tx.Exec(`DELETE FROM chats_fts WHERE id = ?`, chat.ID)
	if err != nil {
		return errors.Wrap(err, "deleting from FTS table")
	}

	_, err = tx.Exec(`INSERT INTO chats_fts (id, searchable_content) VALUES (?, ?)`,
		chat.ID, textToIndex)
	if err != nil {
		return errors.Wrap(err, "inserting into FTS table")
	}

	return tx.Commit()
}

// UpdateChatTitle updates the title of a chat and ensures the FTS table stays in sync.
func (s *Store) UpdateChatTitle(chatID string, title string) error {
	// Fetch the existing chat to recompute its searchable content
	chat, err := s.GetChat(chatID)
	if err != nil {
		return errors.Wrap(err, "retrieving chat for title update")
	}

	// Update the title in the chat object
	chat.Title = &title
	chat.UpdateTimestamp = time.Now().UnixMicro()

	// Recompute the searchable content for the FTS table
	textToIndex, err := s.computeSearchableContent(chat)
	if err != nil {
		return errors.Wrap(err, "computing searchable content")
	}

	// Start a transaction
	tx, err := s.db.Begin()
	if err != nil {
		return errors.Wrap(err, "beginning transaction")
	}
	defer tx.Rollback()

	// Update the title and update_timestamp in the main `chats` table
	_, err = tx.Exec(`
        UPDATE chats
        SET title = ?, update_timestamp = ?
        WHERE id = ?
    `, title, chat.UpdateTimestamp, chat.ID)
	if err != nil {
		return errors.Wrap(err, "updating chat title in database")
	}

	// Update the FTS table using delete and insert
	_, err = tx.Exec(`DELETE FROM chats_fts WHERE id = ?`, chat.ID)
	if err != nil {
		return errors.Wrap(err, "deleting from FTS table")
	}

	_, err = tx.Exec(`INSERT INTO chats_fts (id, searchable_content) VALUES (?, ?)`,
		chat.ID, textToIndex)
	if err != nil {
		return errors.Wrap(err, "inserting into FTS table")
	}

	return tx.Commit()
}

// GetChat retrieves a chat by its ID.
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

// ListChats retrieves a paginated list of chats.
func (s *Store) ListChats(req ListChatsRequest) (*ListChatsResponse, error) {
	var total int
	err := s.db.QueryRow("SELECT COUNT(*) FROM chats").Scan(&total)
	if err != nil {
		return nil, errors.Wrap(err, "counting chats")
	}

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

	if err := rows.Err(); err != nil {
		return nil, errors.Wrap(err, "iterating chat rows")
	}

	return &ListChatsResponse{
		Chats:      chats,
		TotalCount: total,
		PageCount:  pageCount,
	}, nil
}

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

// SearchChats performs a fuzzy search across chat titles and messages.
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
		return nil, errors.Wrap(err, "counting search results")
	}

	pageCount := (total + req.PageSize - 1) / req.PageSize
	offset := (req.Page - 1) * req.PageSize

	rows, err := s.db.Query(`
        SELECT c.id, c.title, c.creation_timestamp, c.update_timestamp, c.messages, c.files
        FROM chats c
        JOIN chats_fts fts ON c.id = fts.id
        WHERE fts.searchable_content MATCH ?
        ORDER BY c.update_timestamp DESC
        LIMIT ? OFFSET ?
    `, req.Query, req.PageSize, offset)
	if err != nil {
		return nil, errors.Wrap(err, "querying search results")
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

	return &SearchChatsResponse{
		Chats:      chats,
		TotalCount: total,
		PageCount:  pageCount,
	}, nil
}
