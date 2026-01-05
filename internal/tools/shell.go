package tools

import (
	"encoding/json"
	"fmt"
	"os/exec"

	aipb "github.com/malonaz/core/genproto/ai/v1"
	jsonpb "github.com/malonaz/core/genproto/json/v1"
)

// ShellCommand defines the tool for executing shell commands.
var ShellCommand = &aipb.Tool{
	Name:        "exec_shell",
	Description: "Execute a shell command on the user's system. Use this when the user asks you to run commands, create files, or perform system operations.",
	JsonSchema: &jsonpb.Schema{
		Type: "object",
		Properties: map[string]*jsonpb.Schema{
			"command": {
				Type:        "string",
				Description: "The shell command to execute",
			},
			"working_directory": {
				Type:        "string",
				Description: "Optional working directory for the command execution. If not specified, uses current directory.",
			},
		},
		Required: []string{"command"},
	},
}

// ShellCommandArgs represents the parsed arguments for shell command execution.
type ShellCommandArgs struct {
	Command          string `json:"command"`
	WorkingDirectory string `json:"working_directory"`
}

// ParseShellCommandArgs parses the JSON arguments for a shell command.
func ParseShellCommandArgs(bytes []byte) (*ShellCommandArgs, error) {
	var args ShellCommandArgs
	if err := json.Unmarshal(bytes, &args); err != nil {
		return nil, fmt.Errorf("failed to parse tool arguments: %w", err)
	}
	if args.Command == "" {
		return nil, fmt.Errorf("no command specified")
	}
	return &args, nil
}

// ExecuteShellCommand executes a shell command and returns the output.
func ExecuteShellCommand(args *ShellCommandArgs) (string, error) {
	cmd := exec.Command("sh", "-c", args.Command)
	if args.WorkingDirectory != "" {
		cmd.Dir = args.WorkingDirectory
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Sprintf("Command failed with error: %v\nOutput: %s", err, string(output)), nil
	}

	return string(output), nil
}
