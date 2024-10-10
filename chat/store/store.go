package store

import (
	"encoding/json"
	"os"
	"path"
	"sort"
	"time"

	"github.com/pkg/errors"

	"github.com/malonaz/sgpt/internal/file"
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

// NewChat instantiates and returns a new chat.
func NewChat(id string) *Chat {
	return &Chat{
		ID:                id,
		CreationTimestamp: time.Now().UnixMicro(),
		UpdateTimestamp:   time.Now().UnixMicro(),
	}
}

// Store implements a local store for chats.
type Store struct {
	path string
}

// New store.
func New(path string) (*Store, error) {
	// Initialize chat directory.
	if err := file.CreateDirectoryIfNotExist(path); err != nil {
		return nil, errors.Wrap(err, "creating directory")
	}

	return &Store{
		path: path,
	}, nil
}

// Write a chat to the store.
func (s *Store) Write(chat *Chat) error {
	chat.UpdateTimestamp = time.Now().UnixMicro()
	path := path.Join(s.path, chat.ID)
	bytes, err := json.MarshalIndent(chat, "", "  ")
	if err != nil {
		return errors.Wrap(err, "marshaling chat to JSON")
	}

	if err := os.WriteFile(path, bytes, 0644); err != nil {
		return errors.Wrap(err, "writing chat to file")
	}
	return nil
}

// Get a chat.
func (s *Store) Get(chatID string) (*Chat, error) {
	path := path.Join(s.path, chatID)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, errors.Errorf("chat not not exist")
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.Wrap(err, "reading chat file")
	}
	chat := &Chat{}
	if err = json.Unmarshal(bytes, chat); err != nil {
		return nil, errors.Wrap(err, "unmarshaling into chat")
	}
	return chat, nil
}

// List all the chats in the store.
func (s *Store) List(pageSize int) ([]*Chat, error) {
	files, err := os.ReadDir(s.path)
	if err != nil {
		return nil, errors.Wrap(err, "listing chats")
	}
	var chats []*Chat
	for _, f := range files {
		fileName := f.Name()
		chat, err := s.Get(fileName)
		if err != nil {
			return nil, errors.Wrapf(err, "getting chat '%s'", fileName)
		}
		chats = append(chats, chat)
	}
	sort.Slice(chats, func(i, j int) bool { return chats[i].UpdateTimestamp > chats[j].UpdateTimestamp })
	if len(chats) < pageSize {
		pageSize = len(chats)
	}
	return chats[:pageSize], nil
}
