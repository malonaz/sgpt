package configuration

import (
	"encoding/json"
	"os"
	"path/filepath"

	"dario.cat/mergo"
	"github.com/pkg/errors"

	"github.com/malonaz/sgpt/internal/file"
)

const (
	providerOpenAI = "open_ai"
)

var defaultConfig = Config{
	Providers: []*Provider{
		{
			Name:           providerOpenAI,
			APIKey:         "API_KEY",
			APIHost:        "https://api.openai.com/v1",
			RequestTimeout: 60,
			Models: []*Model{
				{
					Name:  "gpt-4-0314",
					Alias: "4",
				},
				{
					Name:  "gpt-4-turbo-2024-04-09",
					Alias: "t",
				},
				{
					Name:  "gpt-4o-2024-05-13",
					Alias: "o",
				},
			},
		},
	},

	Chat: &ChatConfig{
		DefaultModel: "gpt-3.5-turbo",
		DefaultRole:  "jcode",
		Directory:    "~/.config/sgpt/chat",
		Roles: []*Role{
			{
				Name:   "CustomRole",
				Alias:  "cr",
				Prompt: "Focus on this or that",
			},
		},
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

type ImageProvider struct {
	APIHost string `json:"api_host"`
	APIKey  string `json:"api_key"`
	Model   string `json:"model"`
}

type Provider struct {
	Name           string `json:"name"`
	APIHost        string `json:"api_host"`
	APIKey         string `json:"api_key"`
	RequestTimeout int    `json:"request_timeout"`
	Anthropic      bool   `json:"anthropic"`

	Models []*Model `json:"models"`
}

// Config holds configuration for the sgpt tool.
type Config struct {
	Providers     []*Provider    `json:"providers"`
	ImageProvider *ImageProvider `json:"image_provider"`
	Diff          *DiffConfig    `json:"diff"`
	Embed         *EmbedConfig   `json:"embed"`
	Chat          *ChatConfig    `json:"chat"`
}

type Model struct {
	Name      string `json:"name"`
	Alias     string `json:"alias"`
	MaxTokens int    `json:"max_tokens"`
}

// ChatConfig holds configuration sgpt chat.
type ChatConfig struct {
	// The model to be used by default.
	DefaultModel string `json:"default_model"`
	// The role to be used by default.
	DefaultRole string `json:"default_role"`
	// The directory where we store chats.
	Directory string `json:"directory"`
	// User defined roles, on top of the built-in roles.
	Roles []*Role `json:"roles"`
	// OpenAI client for images generation.
	ImageProvider *Provider `json:"image_provider"`
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

// Role represents a role.
type Role struct {
	// Name of this role.
	Name string `json:"name"`
	// Alias for this role.
	Alias string `json:"alias"`
	// Prompt for this role.
	Prompt string `json:"description"`
	// Model to use for this role (optional).
	Model string `json:"model"`
	// Files to use for this role (optional).
	Files []string `json:"files"`
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

	// Parse config.
	bytes, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.Wrap(err, "reading file")
	}
	config := &Config{}
	if err = json.Unmarshal(bytes, config); err != nil {
		return nil, errors.Wrap(err, "unmarshaling into config")
	}

	// Parse override configuration if present.
	overrideConfigPath, err := findOverrideConfigPath()
	if err != nil {
		return nil, errors.Wrap(err, "finding override config path")
	}
	if overrideConfigPath != "" {
		bytes, err := os.ReadFile(overrideConfigPath)
		if err != nil {
			return nil, errors.Wrap(err, "reading override config")
		}
		overrideConfig := &Config{}
		if err = json.Unmarshal(bytes, overrideConfig); err != nil {
			return nil, errors.Wrap(err, "unmarshaling into override config")
		}
		mergo.Merge(config, overrideConfig, mergo.WithOverride)
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

func findOverrideConfigPath() (string, error) {
	currentDir, err := os.Getwd()
	if err != nil {
		return "", errors.Wrap(err, "getting working directory")
	}

	for {
		overrideConfigPath := filepath.Join(currentDir, ".sgpt.json")
		ok, err := file.Exists(overrideConfigPath)
		if err != nil {
			return "", errors.Wrap(err, "checking override config existence")
		}

		if ok {
			return overrideConfigPath, nil
		}

		if currentDir == filepath.Dir(currentDir) { // Reached root
			break
		}

		currentDir = filepath.Dir(currentDir)
	}

	return "", nil
}
