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

const (
	// overrideFileName is the committed, repo-local configuration.
	overrideFileName = ".sgpt.json"
	// localOverrideFileName is its git-ignored sibling: personal settings
	// (identity, machine-specific endpoints) that must not be shared.
	localOverrideFileName = ".sgpt.json.local"
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
	overrideConfigPaths, err := findOverrideConfigPaths()
	if err != nil {
		return nil, fmt.Errorf("finding override config paths: %w", err)
	}
	if err := mergeOverrides(configuration, overrideConfigPaths); err != nil {
		return nil, err
	}
	if err := validateGrpcClientReferences(configuration); err != nil {
		return nil, err
	}
	return configuration, nil
}

// mergeOverrides applies the override configurations onto configuration.
// paths are highest precedence first, so they are merged in reverse: the last
// write wins, leaving the cwd-most local override on top.
func mergeOverrides(configuration *sgptpb.Configuration, paths []string) error {
	for i := len(paths) - 1; i >= 0; i-- {
		overrideConfiguration, err := parseConfig(paths[i])
		if err != nil {
			return fmt.Errorf("parsing override config %s: %w", paths[i], err)
		}
		proto.Merge(configuration, overrideConfiguration)
	}
	return nil
}

// GrpcClient resolves a gRPC client by name.
func GrpcClient(configuration *sgptpb.Configuration, name string) (*sgptpb.GrpcClient, error) {
	for _, grpcClient := range configuration.GetGrpcClients() {
		if grpcClient.GetName() == name {
			return grpcClient, nil
		}
	}
	return nil, fmt.Errorf("unknown grpc client: %q", name)
}

// validateGrpcClientReferences ensures every named reference resolves to a
// declared gRPC client, and that declared clients carry an API key (a
// missing env variable silently evaluates to "").
func validateGrpcClientReferences(configuration *sgptpb.Configuration) error {
	for _, grpcClient := range configuration.GetGrpcClients() {
		if grpcClient.GetApiKey() == "" {
			return fmt.Errorf("grpc client %q: empty api_key (is the env variable set?)", grpcClient.GetName())
		}
	}
	aiService := configuration.GetAiService()
	if aiService == "" {
		return fmt.Errorf("ai_service: not set (declare a grpc_clients entry and reference it by name)")
	}
	if _, err := GrpcClient(configuration, aiService); err != nil {
		return fmt.Errorf("ai_service: %w", err)
	}
	return nil
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

// overrideConfigPathsIn returns the override configurations present in dir,
// ordered from highest to lowest precedence: .sgpt.json.local is git-ignored
// and personal, so it wins over the committed .sgpt.json beside it.
func overrideConfigPathsIn(dir string) ([]string, error) {
	var paths []string
	for _, name := range []string{localOverrideFileName, overrideFileName} {
		path := filepath.Join(dir, name)
		ok, err := file.Exists(path)
		if err != nil {
			return nil, fmt.Errorf("checking override config existence: %w", err)
		}
		if ok {
			paths = append(paths, path)
		}
	}
	return paths, nil
}

// findOverrideConfigPaths walks up from cwd collecting every override
// configuration. Returns them ordered from highest to lowest precedence:
// cwd-most before root-most, and within a directory the local override before
// the committed one.
func findOverrideConfigPaths() ([]string, error) {
	currentDir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getting working directory: %w", err)
	}

	var paths []string
	for {
		dirPaths, err := overrideConfigPathsIn(currentDir)
		if err != nil {
			return nil, err
		}
		paths = append(paths, dirPaths...)
		if currentDir == filepath.Dir(currentDir) {
			break
		}
		currentDir = filepath.Dir(currentDir)
	}

	return paths, nil
}

func parseConfig(path string) (*sgptpb.Configuration, error) {
	content, err := jsonnet.EvaluateFile(path, jsonnet.WithEnvVariables())
	if err != nil {
		return nil, fmt.Errorf("evaluating config: %w", err)
	}

	configuration := &sgptpb.Configuration{}
	// Strict: unknown fields (e.g. the removed role/tool-set formats) are
	// errors, never silently dropped.
	if err := pbutil.JSONUnmarshalStrict(content, configuration); err != nil {
		return nil, fmt.Errorf("unmarshaling into config: %w", err)
	}
	return configuration, nil
}

// LoadIgnore returns the top-level ignore patterns of the repo-local
// configuration at root, if any. Used when scanning imported repos: an
// import obeys its own ignores, never the importer's.
//
// Both override files contribute: ignores accumulate rather than override, so
// a .sgpt.json.local adds to what .sgpt.json already excludes.
func LoadIgnore(root string) []string {
	paths, err := overrideConfigPathsIn(root)
	if err != nil {
		return nil
	}
	var ignore []string
	for _, path := range paths {
		configuration, err := parseConfig(path)
		if err != nil {
			return nil
		}
		ignore = append(ignore, configuration.GetIgnore()...)
	}
	return ignore
}
