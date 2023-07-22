package store

import (
	"encoding/json"
	"fmt"
	"os"
)

// File represents a file.
type File struct {
	Name             string
	Hash             string
	Chunks  []*FileChunk
	CreationTimestamp uint64
}

// FileChunk represents a file chunk.
type FileChunk struct {
	Embedding         []float32
	Filename          string
	Content string
}

// Store represents the data layer that stores files on disk.
type Store struct {
	filepath string
	files    map[string]*File
}

// Load creates a new data layer instance.
func Load(filepath string) (*Store, error) {
	s := &Store{
		filepath: filepath,
		files:    make(map[string]*File),
	}
	return s, s.load()
}

// Save saves the current data layer state to disk.
func (s *Store) Save() error {
	data, err := json.Marshal(s.files)
	if err != nil {
		return fmt.Errorf("failed to marshal files to JSON: %w", err)
	}
	err = os.WriteFile(s.filepath, data, 0644)
	if err != nil {
		return fmt.Errorf("failed to write data to disk: %w", err)
	}
	return nil
}

// load loads the data layer state from disk.
func (s *Store) load() error {
	_, err := os.Stat(s.filepath)
	if os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("failed to check data file: %w", err)
	}
	data, err := os.ReadFile(s.filepath)
	if err != nil {
		return fmt.Errorf("failed to read data file: %w", err)
	}
	err = json.Unmarshal(data, &s.files)
	if err != nil {
		return fmt.Errorf("failed to unmarshal data from JSON: %w", err)
	}
	return nil
}

// AddFile adds a file to the data layer.
func (s *Store) AddFile(file *File) {
	s.files[file.Name] = file
}

// GetFile retrieves a file from the data layer using its name.
func (s *Store) GetFile(name string) (*File, bool) {
	file, ok := s.files[name]
	return file, ok
}
