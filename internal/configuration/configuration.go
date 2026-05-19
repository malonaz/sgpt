package configuration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	if err := resolveRoleFilePaths(configuration, filepath.Dir(path)); err != nil {
		return nil, fmt.Errorf("resolving role file paths in %s: %w", path, err)
	}

	overrideConfigPaths, err := findOverrideConfigPaths()
	if err != nil {
		return nil, fmt.Errorf("finding override config paths: %w", err)
	}
	// Apply overrides from root-most to cwd-most so closer overrides win.
	for i := len(overrideConfigPaths) - 1; i >= 0; i-- {
		overrideConfiguration, err := parseConfig(overrideConfigPaths[i])
		if err != nil {
			return nil, fmt.Errorf("parsing override config %s: %w", overrideConfigPaths[i], err)
		}
		if err := resolveRoleFilePaths(overrideConfiguration, filepath.Dir(overrideConfigPaths[i])); err != nil {
			return nil, fmt.Errorf("resolving role file paths in %s: %w", overrideConfigPaths[i], err)
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

// resolveRoleFilePaths converts relative paths in role files to absolute paths
// relative to the config directory that defined them. Returns an error if any
// path does not exist.
func resolveRoleFilePaths(configuration *sgptpb.Configuration, configDir string) error {
	for _, r := range configuration.GetChat().GetRoles() {
		for i, f := range r.GetFiles() {
			expanded, err := file.ExpandPath(f)
			if err != nil {
				return fmt.Errorf("role %q: expanding path %q: %w", r.GetName(), f, err)
			}
			r.Files[i] = expanded
			if !filepath.IsAbs(r.Files[i]) {
				r.Files[i] = filepath.Join(configDir, r.Files[i])
			}
			checkPath := strings.TrimSuffix(r.Files[i], "/...")
			if _, err := os.Stat(checkPath); err != nil {
				return fmt.Errorf("role %q references non-existent path %q: %w", r.GetName(), r.Files[i], err)
			}
		}
	}
	return nil
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

// findOverrideConfigPaths walks up from cwd collecting all sgpt.json files.
// Returns them ordered from cwd-most to root-most.
func findOverrideConfigPaths() ([]string, error) {
	currentDir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getting working directory: %w", err)
	}

	var paths []string
	for {
		overrideConfigPath := filepath.Join(currentDir, "sgpt.json")
		ok, err := file.Exists(overrideConfigPath)
		if err != nil {
			return nil, fmt.Errorf("checking override config existence: %w", err)
		}
		if ok {
			paths = append(paths, overrideConfigPath)
		}
		if currentDir == filepath.Dir(currentDir) {
			break
		}
		currentDir = filepath.Dir(currentDir)
	}

	return paths, nil
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
