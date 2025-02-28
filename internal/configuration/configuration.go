package configuration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"dario.cat/mergo"
	"github.com/google/go-jsonnet"
	"github.com/pkg/errors"

	"github.com/malonaz/sgpt/internal/file"
)

const (
	providerOpenAI = "open_ai"
)

var defaultConfig = Config{
	Database: "~/.config/sgpt/sgpt.db",
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
	Database      string         `json:"database"`
	Providers     []*Provider    `json:"providers"`
	ImageProvider *ImageProvider `json:"image_provider"`
	Diff          *DiffConfig    `json:"diff"`
	Embed         *EmbedConfig   `json:"embed"`
	Chat          *ChatConfig    `json:"chat"`
}

type Model struct {
	Name           string `json:"name"`
	Alias          string `json:"alias"`
	MaxTokens      int    `json:"max_tokens"`
	ThinkingTokens int    `json:"thinking_tokens"`
}

// ChatConfig holds configuration sgpt chat.
type ChatConfig struct {
	// The model used to generate summaries.
	SummaryModel string `json:"summary_model"`
	// The model to be used by default.
	DefaultModel string `json:"default_model"`
	// The role to be used by default.
	DefaultRole string `json:"default_role"`
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
	Prompt string `json:"prompt"`
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

	config, err := parseConfig(path)
	if err != nil {
		return nil, errors.Wrapf(err, "parsing config %s)", path)
	}

	// Parse override configuration if present.
	overrideConfigPath, err := findOverrideConfigPath()
	if err != nil {
		return nil, errors.Wrap(err, "finding override config path")
	}
	if overrideConfigPath != "" {
		overrideConfig, err := parseConfig(overrideConfigPath)
		if err != nil {
			return nil, errors.Wrapf(err, "parsing override config %s)", overrideConfigPath)
		}
		mergo.Merge(config, overrideConfig, mergo.WithOverride)
	}

	expandedDatabasePath, err := file.ExpandPath(config.Database)
	if err != nil {
		return nil, errors.Wrap(err, "expanding database path")
	}
	config.Database = expandedDatabasePath

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

func parseConfig(filepath string) (*Config, error) {
	content, err := evaluateFile(filepath)
	if err != nil {
		return nil, errors.Wrap(err, "evaluating config")
	}

	config := &Config{}
	if err = json.Unmarshal([]byte(content), config); err != nil {
		return nil, errors.Wrap(err, "unmarshaling into config")
	}
	return config, nil
}

func evaluateFile(filePath string) (string, error) {
	// Create a new Jsonnet VM
	vm := jsonnet.MakeVM()

	// Set the import callback to handle relative imports
	vm.Importer(&jsonnet.FileImporter{
		JPaths: []string{filepath.Dir(filePath)},
	})

	// Evaluate the Jsonnet file
	jsonStr, err := vm.EvaluateFile(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to evaluate Jsonnet file: %v", err)
	}

	return jsonStr, nil
}
