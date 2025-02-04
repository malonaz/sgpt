package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

// Store implements a SQLite store for chats.
type Store struct {
	db *sql.DB
}

// New initializes a new Store with the specified SQLite database path.
func New(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening database: %%w", err)
	}

	// Create the main chats table if it doesn't exist
	_, err = db.Exec(`
        CREATE TABLE IF NOT EXISTS chats (
            id TEXT PRIMARY KEY,
            title TEXT,
            creation_timestamp INTEGER NOT NULL,
            update_timestamp INTEGER NOT NULL,
            messages TEXT NOT NULL,
            files TEXT NOT NULL DEFAULT '[]',
            favorite INTEGER NOT NULL DEFAULT 0
        )
    `)
	if err != nil {
		return nil, fmt.Errorf("creating chats table: %%w", err)
	}

	// Check if favorite column exists
	var exists int
	err = db.QueryRow(`
    SELECT COUNT(*) FROM pragma_table_info('chats') WHERE name='favorite'
`).Scan(&exists)
	if err != nil {
		return nil, fmt.Errorf("checking favorite column: %%w", err)
	}
	// Add favorite column if it doesn't exist
	if exists == 0 {
		_, err = db.Exec(`
        ALTER TABLE chats ADD COLUMN favorite INTEGER NOT NULL DEFAULT 0
    `)
		if err != nil {
			return nil, fmt.Errorf("adding favorite column: %%w", err)
		}
	}

	err = db.QueryRow(`
    SELECT COUNT(*) FROM pragma_table_info('chats') WHERE name='tags'
`).Scan(&exists)
	if err != nil {
		return nil, fmt.Errorf("checking tags column: %w", err)
	}
	// Add tags column if it doesn't exist
	if exists == 0 {
		_, err = db.Exec(`
        ALTER TABLE chats ADD COLUMN tags TEXT NOT NULL DEFAULT '[]'
    `)
		if err != nil {
			return nil, fmt.Errorf("adding tags column: %w", err)
		}
	}

	// Create the FTS table
	_, err = db.Exec(`
        CREATE VIRTUAL TABLE IF NOT EXISTS chats_fts USING fts5(
            id UNINDEXED,
            searchable_content
        )
    `)
	if err != nil {
		return nil, fmt.Errorf("creating FTS5 table: %%w", err)
	}

	return &Store{db: db}, nil
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
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

func (s *Store) GetChat(chatID string) (*Chat, error) {
	row := s.db.QueryRow(`
        SELECT id, title, creation_timestamp, update_timestamp, messages, files, favorite
        FROM chats
        WHERE id = ?
    `, chatID)

	chat, err := scanChat(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("chat not found")
		}
		return nil, fmt.Errorf("querying chat: %%w", err)
	}

	return chat, nil
}

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

	// Add tags filtering
	if len(req.Tags) > 0 {
		if whereClause.Len() > 0 {
			whereClause.WriteString(" AND ")
		}
		tagsJSON, err := json.Marshal(dedupeStringsSorted(req.Tags))
		if err != nil {
			return nil, fmt.Errorf("marshaling tags filter: %w", err)
		}
		// Use JSON_EACH to handle array containment
		whereClause.WriteString(`
            EXISTS (
                SELECT 1 FROM json_each(tags)
                WHERE value IN (SELECT value FROM json_each(?))
            )
        `)
		args = append(args, string(tagsJSON))
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
        SELECT c.id, c.title, c.creation_timestamp, c.update_timestamp, c.messages, c.files, c.favorite
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
