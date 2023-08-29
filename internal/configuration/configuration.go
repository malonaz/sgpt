package configuration

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/malonaz/sgpt/internal/file"
	"github.com/pkg/errors"
)

var defaultConfig = Config{
	OpenaiAPIKey:   "API_KEY",
	OpenaiAPIHost:  "https://api.openai.com",
	RequestTimeout: 60,

	Chat: &ChatConfig{
		DefaultModel: "gpt-3.5-turbo",
		Directory:    "~/.config/sgpt/chat",
	},

	Diff: &DiffConfig{
		DefaultModel: "gpt-3.5-turbo",
		IgnoreFiles:  []string{},
	},

	Embed: &EmbedConfig{
		Directory:      "~/.config/sgpt/embed",
		IgnoreFiles:    []string{},
		FileExtensions: []string{},
	},
}

// Config holds configuration for the sgpt tool.
type Config struct {
	OpenaiAPIKey   string `json:"openai_api_key"`
	OpenaiAPIHost  string `json:"openai_api_host"`
	RequestTimeout int    `json:"request_timeout"`

	Diff  *DiffConfig  `json:"diff"`
	Embed *EmbedConfig `json:"embed"`
	Chat  *ChatConfig  `json:"chat"`
}

// ChatConfig holds configuration sgpt chat.
type ChatConfig struct {
	// The model to be used by the configuration.
	DefaultModel string `json:"default_model"`
	// The directory where we store chats.
	Directory string `json:"directory"`
}

// DiffConfig holds configuration sgpt diff.
type DiffConfig struct {
	// The model to be used by the configuration.
	DefaultModel string `json:"default_model"`
	// We ignore files here.
	IgnoreFiles []string `json:"ignore_files"`
}

// EmbedConfig holds configuration sgpt embed.
type EmbedConfig struct {
	// The model to be used by the configuration.
	DefaultModel string `json:"default_model"`
	// The directory where we store embeds.
	Directory string `json:"directory"`
	// We only embed files with the given extensions.
	FileExtensions []string `json:"file_extensions"`
	// We ignore these files.
	IgnoreFiles []string `json:"ignore_files"`
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

	expandedChatDirectoryPath, err := file.ExpandPath(config.Chat.Directory)
	if err != nil {
		return nil, errors.Wrap(err, "expanding chat directory path")
	}
	config.Chat.Directory = expandedChatDirectoryPath

	expandedEmbedDirectoryPath, err := file.ExpandPath(config.Embed.Directory)
	if err != nil {
		return nil, errors.Wrap(err, "expanding embed directory path")
	}
	config.Embed.Directory = expandedEmbedDirectoryPath
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
