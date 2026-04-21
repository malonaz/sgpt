package configuration

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/malonaz/core/go/jsonnet"
	"github.com/malonaz/core/go/pbutil"
	"google.golang.org/protobuf/proto"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/file"
)

var defaultConfig = &sgptpb.Configuration{
	Models: []*sgptpb.Model{
		{Name: "providers/openai/models/gpt-4", Alias: "4"},
		{Name: "providers/openai/models/gpt-4-turbo", Alias: "t"},
		{Name: "providers/openai/models/gpt-4o", Alias: "o"},
		{Name: "providers/openai/models/gpt-3.5-turbo", Alias: "3"},
	},
	Chat: &sgptpb.ChatConfiguration{
		DefaultModel: "providers/openai/models/gpt-4o",
		SummaryModel: "providers/openai/models/gpt-3.5-turbo",
		Roles: []*sgptpb.Role{
			{Name: "CustomRole", Alias: "cr", Prompt: "Focus on this or that"},
		},
	},
}

// Parse a configuration file.
func Parse(path string) (*sgptpb.Configuration, error) {
	path, err := file.ExpandPath(path)
	if err != nil {
		return nil, fmt.Errorf("expanding path: %w", err)
	}
	if err := initializeIfNotPresent(path); err != nil {
		return nil, fmt.Errorf("initializing configuration: %w", err)
	}

	configuration, err := parseConfig(path)
	if err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}

	overrideConfigPath, err := findOverrideConfigPath()
	if err != nil {
		return nil, fmt.Errorf("finding override config path: %w", err)
	}
	if overrideConfigPath != "" {
		overrideConfiguration, err := parseConfig(overrideConfigPath)
		if err != nil {
			return nil, fmt.Errorf("parsing override config %s: %w", overrideConfigPath, err)
		}
		proto.Merge(configuration, overrideConfiguration)
	}
	return configuration, nil
}

// ResolveModelAlias resolves a model name or alias to the full model name.
func ResolveModelAlias(configuration *sgptpb.Configuration, nameOrAlias string) (string, error) {
	if filepath.Base(nameOrAlias) != nameOrAlias {
		return nameOrAlias, nil
	}

	for _, model := range configuration.GetModels() {
		if model.GetAlias() == nameOrAlias {
			return model.GetName(), nil
		}
	}

	for _, model := range configuration.GetModels() {
		if model.GetName() == nameOrAlias {
			return model.GetName(), nil
		}
	}

	return "", fmt.Errorf("unknown model alias or name: %s", nameOrAlias)
}

func save(configuration *sgptpb.Configuration, path string) error {
	bytes, err := pbutil.JSONMarshalPretty(configuration)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	if err := os.WriteFile(path, bytes, 0644); err != nil {
		return fmt.Errorf("writing file: %w", err)
	}
	return nil
}

func initializeIfNotPresent(path string) error {
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		return nil
	}

	dir, _ := filepath.Split(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating folders: %w", err)
	}

	if err := save(defaultConfig, path); err != nil {
		return fmt.Errorf("saving default config: %w", err)
	}
	return nil
}

func findOverrideConfigPath() (string, error) {
	currentDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getting working directory: %w", err)
	}

	for {
		overrideConfigPath := filepath.Join(currentDir, ".sgpt.json")
		ok, err := file.Exists(overrideConfigPath)
		if err != nil {
			return "", fmt.Errorf("checking override config existence: %w", err)
		}
		if ok {
			return overrideConfigPath, nil
		}
		if currentDir == filepath.Dir(currentDir) {
			break
		}
		currentDir = filepath.Dir(currentDir)
	}

	return "", nil
}

func parseConfig(path string) (*sgptpb.Configuration, error) {
	content, err := jsonnet.EvaluateFile(path)
	if err != nil {
		return nil, fmt.Errorf("evaluating config: %w", err)
	}

	configuration := &sgptpb.Configuration{}
	if err := pbutil.JSONUnmarshal(content, configuration); err != nil {
		return nil, fmt.Errorf("unmarshaling into config: %w", err)
	}
	return configuration, nil
}
