package configuration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"dario.cat/mergo"
	"github.com/malonaz/core/go/jsonnet"
	"github.com/pkg/errors"

	"github.com/malonaz/sgpt/internal/file"
)

var defaultConfig = Config{
	Models: []*Model{
		{
			Name:  "providers/openai/models/gpt-4",
			Alias: "4",
		},
		{
			Name:  "providers/openai/models/gpt-4-turbo",
			Alias: "t",
		},
		{
			Name:  "providers/openai/models/gpt-4o",
			Alias: "o",
		},
		{
			Name:  "providers/openai/models/gpt-3.5-turbo",
			Alias: "3",
		},
	},
	Chat: &ChatConfig{
		DefaultModel: "providers/openai/models/gpt-4o",
		DefaultRole:  "",
		SummaryModel: "providers/openai/models/gpt-3.5-turbo",
		Roles: []*Role{
			{
				Name:   "CustomRole",
				Alias:  "cr",
				Prompt: "Focus on this or that",
			},
		},
	},
}

// Model represents a model with an optional alias for convenience.
type Model struct {
	Name  string `json:"name"`
	Alias string `json:"alias"`
}

type GRPCClient struct {
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
}

// Config holds configuration for the sgpt tool.
type Config struct {
	ChatService *GRPCClient `json:"chat_service"`
	AiService   *GRPCClient `json:"ai_service"`
	Models      []*Model    `json:"models"`
	Chat        *ChatConfig `json:"chat"`
}

// ChatConfig holds configuration for sgpt chat.
type ChatConfig struct {
	// The model used to generate summaries.
	SummaryModel string `json:"summary_model"`
	// The model to be used by default.
	DefaultModel string `json:"default_model"`
	// The role to be used by default.
	DefaultRole string `json:"default_role"`
	// User defined roles, on top of the built-in roles.
	Roles []*Role `json:"roles"`
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
		return nil, errors.Wrapf(err, "parsing config %s", path)
	}

	// Parse override configuration if present.
	overrideConfigPath, err := findOverrideConfigPath()
	if err != nil {
		return nil, errors.Wrap(err, "finding override config path")
	}
	if overrideConfigPath != "" {
		overrideConfig, err := parseConfig(overrideConfigPath)
		if err != nil {
			return nil, errors.Wrapf(err, "parsing override config %s", overrideConfigPath)
		}
		if err := mergo.Merge(config, overrideConfig, mergo.WithOverride); err != nil {
			return nil, errors.Wrap(err, "merging override config")
		}
	}
	return config, nil
}

// ResolveModelAlias resolves a model name or alias to the full model name.
// If the input is already a full model name (contains "/"), returns it unchanged.
// If it's an alias, returns the corresponding full model name.
// Returns error if alias is not found.
func (c *Config) ResolveModelAlias(nameOrAlias string) (string, error) {
	// If it contains "/", assume it's already a full model name
	if filepath.Base(nameOrAlias) != nameOrAlias {
		return nameOrAlias, nil
	}

	// Try to find by alias first, then by name
	for _, model := range c.Models {
		if model.Alias == nameOrAlias {
			return model.Name, nil
		}
	}

	for _, model := range c.Models {
		if model.Name == nameOrAlias {
			return model.Name, nil
		}
	}

	return "", fmt.Errorf("unknown model alias or name: %s", nameOrAlias)
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
	content, err := jsonnet.EvaluateFile(filepath)
	if err != nil {
		return nil, errors.Wrap(err, "evaluating config")
	}

	config := &Config{}
	if err = json.Unmarshal(content, config); err != nil {
		return nil, errors.Wrap(err, "unmarshaling into config")
	}
	return config, nil
}
