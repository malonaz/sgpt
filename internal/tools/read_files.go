package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	aipb "github.com/malonaz/core/genproto/ai/v1"
)

// ReadFilesTool defines the tool for reading file contents.
var ReadFiles = &aipb.Tool{
	Name:        "read_files",
	Description: "Read the contents of one or more files. Use this to examine file contents before making changes or to understand code structure.",
	JsonSchema: &aipb.JsonSchema{
		Type: "object",
		Properties: map[string]*aipb.JsonSchema{
			"paths": {
				Type:        "array",
				Description: "List of file paths to read",
				Items:       &aipb.JsonSchema{Type: "string"},
			},
		},
		Required: []string{"paths"},
	},
}

// ReadFilesArgs represents the parsed arguments for reading files.
type ReadFilesArgs struct {
	Paths []string `json:"paths"`
}

// ParseReadFilesArgs parses the JSON arguments for reading files.
func ParseReadFilesArgs(argumentsJSON string) (*ReadFilesArgs, error) {
	var args ReadFilesArgs
	if err := json.Unmarshal([]byte(argumentsJSON), &args); err != nil {
		return nil, fmt.Errorf("failed to parse tool arguments: %w", err)
	}
	if len(args.Paths) == 0 {
		return nil, fmt.Errorf("no paths specified")
	}
	return &args, nil
}

// ExecuteReadFiles reads the specified files and returns their contents.
func ExecuteReadFiles(args *ReadFilesArgs) (string, error) {
	var results []string
	for _, path := range args.Paths {
		content, err := os.ReadFile(path)
		if err != nil {
			results = append(results, fmt.Sprintf("=== %s ===\nError: %v", path, err))
		} else {
			results = append(results, fmt.Sprintf("=== %s ===\n%s", path, string(content)))
		}
	}
	return strings.Join(results, "\n\n"), nil
}
