package configuration

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/malonaz/sgpt/file"
	"github.com/pkg/errors"
)

var defaultConfig = Config{
	OpenaiAPIKey:    "API_KEY",
	OpenaiAPIHost:   "https://api.openai.com",
	RequestTimeout:  60,
	DefaultModel:    "gpt-3.5-turbo",
	ChatDirectory:   "~/.sgpt/chats",
	DiffIgnoreFiles: []string{},
}

// Config holds configuration for the sgpt tool.
type Config struct {
	OpenaiAPIKey    string   `json:"openai_api_key"`
	OpenaiAPIHost   string   `json:"openai_api_host"`
	RequestTimeout  int      `json:"request_timeout"`
	DefaultModel    string   `json:"default_model"`
	ChatDirectory   string   `json:"chat_directory"`
	DiffIgnoreFiles []string `json:"diff_ignore_files"`
	EmbedFileExtensions []string `json:"embed_file_extensions"`
}

// Parse a configuration file.
func Parse(path string) (*Config, error) {
	path, err := file.ExpandPath(path)
	if err != nil {
		return nil, errors.Wrap(err, "expanding path")
	}

	if err := initializeIfNotPresent(path); err != nil {
		return nil, errors.Wrap(err, "initializing configuration")
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.Wrap(err, "reading file")
	}

	config := &Config{}
	if err = json.Unmarshal(bytes, config); err != nil {
		return nil, errors.Wrap(err, "unmarshaling into config")
	}

	expandedChatDirectoryPath, err := file.ExpandPath(config.ChatDirectory)
	if err != nil {
		return nil, errors.Wrap(err, "expanding chat directory path")
	}
	config.ChatDirectory = expandedChatDirectoryPath
	return config, nil
}

// save a configuration file.
func (c *Config) save(path string) error {
	bytes, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return errors.Wrap(err, "marshaling config")
	}

	err = os.WriteFile(path, bytes, 0644)
	if err != nil {
		return errors.Wrap(err, "writing file")
	}

	return nil
}

// initializeIfNotPresent initializes a config if it does not exist.
func initializeIfNotPresent(path string) error {
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		return nil
	}

	// Create the directories.
	dir, _ := filepath.Split(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return errors.Wrap(err, "creating folders")
	}

	if err := defaultConfig.save(path); err != nil {
		return errors.Wrap(err, "saving default config")
	}
	return nil
}
