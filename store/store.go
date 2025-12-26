package store

import (
	"database/sql"
	"fmt"

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
		return nil, fmt.Errorf("opening database: %w", err)
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
		return nil, fmt.Errorf("creating chats table: %w", err)
	}

	// Check if favorite column exists
	var exists int
	err = db.QueryRow(`
    SELECT COUNT(*) FROM pragma_table_info('chats') WHERE name='favorite'
`).Scan(&exists)
	if err != nil {
		return nil, fmt.Errorf("checking favorite column: %w", err)
	}
	// Add favorite column if it doesn't exist
	if exists == 0 {
		_, err = db.Exec(`
        ALTER TABLE chats ADD COLUMN favorite INTEGER NOT NULL DEFAULT 0
    `)
		if err != nil {
			return nil, fmt.Errorf("adding favorite column: %w", err)
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
		return nil, fmt.Errorf("creating FTS5 table: %w", err)
	}

	return &Store{db: db}, nil
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// computeSearchableContent concatenates the chat title and all message contents into a single searchable string.
func (s *Store) computeSearchableContent(chat *Chat) (string, error) {
	return "", nil
}
