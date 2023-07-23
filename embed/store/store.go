package store

import (
	"math"
	"encoding/json"
	"fmt"
	"sort"
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


// Search this store's embeddings, returning the most similar.
func (s *Store) Search(vector []float32) ([]*FileChunk, error) {
	fileChunks := []*FileChunk{}
	for _, file := range s.files {
		fileChunks = append(fileChunks, file.Chunks...)
	}
	var err error
	sort.Slice(fileChunks, func(i, j int) bool {
		var distanceA, distanceB float64
		distanceA, err = cosine(fileChunks[i].Embedding, vector)
		if err != nil {
			return false
		}
		distanceB, err = cosine(fileChunks[j].Embedding, vector)
		if err != nil {
			return false
		}
		return distanceA < distanceB
	})
	return fileChunks, err
}

// Cosine distance.
func cosine(originalA []float32, originalB []float32) (cosine float64, err error) {
	a := make([]float64, len(originalA))
	b := make([]float64, len(originalA))
	for i := 0; i < len(originalA); i++ {
		a[i] = float64(originalA[i])
		b[i] = float64(originalB[i])
	}
	sumA := 0.0
	s1 := 0.0
	s2 := 0.0
	for k := 0; k < len(a); k++ {
		sumA += a[k] * b[k]
		s1 += math.Pow(a[k], 2)
		s2 += math.Pow(b[k], 2)
	}
	if s1 == 0 || s2 == 0 {
		return 0.0, fmt.Errorf("Vectors should not be null (all zeros)")
	}
	return sumA / (math.Sqrt(s1) * math.Sqrt(s2)), nil
}
