package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	aipb "github.com/malonaz/core/genproto/ai/v1"
	jsonpb "github.com/malonaz/core/genproto/json/v1"
)

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
	Annotations: map[string]string{
		ToolHandlerIDAnnotation: HandlerIDShell,
	},
}

type ShellCommandArgs struct {
	Command          string `json:"command"`
	WorkingDirectory string `json:"working_directory"`
}

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

type ShellHandler struct{}

func (h *ShellHandler) HandleToolCall(_ context.Context, toolCall *aipb.ToolCall) (*HandleResult, error) {
	bytes, err := json.Marshal(toolCall.Arguments.AsMap())
	if err != nil {
		return nil, fmt.Errorf("marshaling tool call arguments: %w", err)
	}
	args, err := ParseShellCommandArgs(bytes)
	if err != nil {
		return nil, err
	}
	display := args.Command
	if args.WorkingDirectory != "" {
		display = fmt.Sprintf("cd %s && %s", args.WorkingDirectory, args.Command)
	}
	return &HandleResult{Display: display, AutoExecute: false}, nil
}

func (h *ShellHandler) ProcessToolCall(_ context.Context, toolCall *aipb.ToolCall) (*aipb.ToolResult, error) {
	bytes, err := json.Marshal(toolCall.Arguments.AsMap())
	if err != nil {
		return nil, fmt.Errorf("marshaling tool call arguments: %w", err)
	}
	args, err := ParseShellCommandArgs(bytes)
	if err != nil {
		return nil, err
	}
	result, err := ExecuteShellCommand(args)
	if err != nil {
		return nil, err
	}
	return &aipb.ToolResult{
		ToolName:   toolCall.Name,
		ToolCallId: toolCall.Id,
		Result:     &aipb.ToolResult_Content{Content: result},
	}, nil
}
