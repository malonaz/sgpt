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
		distanceA, err = cosineDistance(fileChunks[i].Embedding, vector)
		if err != nil {
			return false
		}
		distanceB, err = cosineDistance(fileChunks[j].Embedding, vector)
		if err != nil {
			return false
		}
		return distanceA < distanceB
	})
	return fileChunks, err
}

func cosineSimilarity(a, b []float32) (float64, error) {
	if len(a) != len(b) {
		return 0, fmt.Errorf("Vectors must have the same dimensions")
	}

	vec1 := make([]float64, len(a))
	vec2 := make([]float64, len(b))
	for i := 0; i < len(a); i++ {
		vec1[i] = float64(a[i])
		vec2[i] = float64(b[i])
	}

	dotProduct := 0.0
	magnitude1 := 0.0
	magnitude2 := 0.0
	for i := 0; i < len(vec1); i++ {
		dotProduct += vec1[i] * vec2[i]
		magnitude1 += vec1[i] * vec1[i]
		magnitude2 += vec2[i] * vec2[i]
	}
	magnitude1 = math.Sqrt(magnitude1)
	magnitude2 = math.Sqrt(magnitude2)
	if magnitude1 == 0 || magnitude2 == 0 {
		return 0, fmt.Errorf("One or both of the vectors have zero magnitude")
	}
	return dotProduct / (magnitude1 * magnitude2), nil
}

func cosineDistance(vec1, vec2 []float32) (float64, error) {
	similarity, err := cosineSimilarity(vec1, vec2)
	if err != nil {
		return 0, err
	}
	return 1 - similarity, nil
}
