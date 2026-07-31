package shell

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	aipb "github.com/malonaz/core/genproto/ai/v1"
	jsonpb "github.com/malonaz/core/genproto/json/v1"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/tool"
)

// Definition is the tool definition for shell execution.
var Definition = &aipb.Tool{
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
		tool.ToolHandlerIDAnnotation: tool.HandlerIDShell,
	},
}

type shellCommandArguments struct {
	Command          string `json:"command"`
	WorkingDirectory string `json:"working_directory"`
}

func parseShellCommandArguments(toolCall *aipb.ToolCall) (*shellCommandArguments, error) {
	bytes, err := tool.ArgumentsJSON(toolCall)
	if err != nil {
		return nil, err
	}
	arguments := &shellCommandArguments{}
	if err := json.Unmarshal(bytes, arguments); err != nil {
		return nil, fmt.Errorf("parsing tool arguments: %w", err)
	}
	if arguments.Command == "" {
		return nil, fmt.Errorf("no command specified")
	}
	return arguments, nil
}

// Tool executes shell commands on the user's system.
type Tool struct{}

func (t *Tool) Review(_ context.Context, toolCall *aipb.ToolCall) (*sgptpb.ToolCallMetadata, error) {
	arguments, err := parseShellCommandArguments(toolCall)
	if err != nil {
		return nil, err
	}
	display := arguments.Command
	if arguments.WorkingDirectory != "" {
		display = fmt.Sprintf("cd %s && %s", arguments.WorkingDirectory, arguments.Command)
	}
	// Shell commands are arbitrary code execution: never auto-execute.
	return &sgptpb.ToolCallMetadata{
		DisplayMessage: &sgptpb.DisplayMessage{Content: display},
	}, nil
}

func (t *Tool) Execute(_ context.Context, toolCall *aipb.ToolCall) (*aipb.ToolResult, error) {
	arguments, err := parseShellCommandArguments(toolCall)
	if err != nil {
		return nil, err
	}
	command := exec.Command("sh", "-c", arguments.Command)
	if arguments.WorkingDirectory != "" {
		command.Dir = arguments.WorkingDirectory
	}
	output, err := command.CombinedOutput()
	content := string(output)
	if err != nil {
		// Surface failures as content so the model can react to them.
		content = fmt.Sprintf("Command failed with error: %v\nOutput: %s", err, string(output))
	}
	return &aipb.ToolResult{
		ToolName:   toolCall.Name,
		ToolCallId: toolCall.Id,
		Result:     &aipb.ToolResult_Content{Content: content},
	}, nil
}

var _ tool.Tool = (*Tool)(nil)

// RenderRequest renders the command as a shell fence instead of raw JSON.
func (t *Tool) RenderRequest(toolCall *aipb.ToolCall) (string, bool) {
	arguments, err := parseShellCommandArguments(toolCall)
	if err != nil {
		return "", false
	}
	display := arguments.Command
	if arguments.WorkingDirectory != "" {
		display = fmt.Sprintf("cd %s && %s", arguments.WorkingDirectory, arguments.Command)
	}
	return fmt.Sprintf("```sh\n%s\n```", display), true
}

// RenderHeader: the command itself renders below (display message + fence),
// so the header stays a discrete label.
func (t *Tool) RenderHeader(*aipb.ToolCall) (string, bool) {
	return "💻 shell", true
}

var (
	_ tool.RequestRenderer = (*Tool)(nil)
	_ tool.HeaderRenderer  = (*Tool)(nil)
)

func init() { tool.RegisterBuiltin(Definition) }
