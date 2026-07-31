package shell

import (
	"context"
	"errors"
	"fmt"
	"os/exec"

	aipb "github.com/malonaz/core/genproto/ai/v1"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/tool"
)

// Definition is the tool definition for shell execution, built from the
// ToolService.ExecShell method.
var Definition = tool.MustBuildTool("exec_shell", tool.HandlerIDShell, "sgpt.v1.ToolService.ExecShell")

func parseShellCommandArguments(toolCall *aipb.ToolCall) (*sgptpb.ExecShellRequest, error) {
	execShellRequest := &sgptpb.ExecShellRequest{}
	if err := tool.UnmarshalArguments(toolCall, execShellRequest); err != nil {
		return nil, err
	}
	if execShellRequest.GetCommand() == "" {
		return nil, fmt.Errorf("no command specified")
	}
	return execShellRequest, nil
}

// Tool executes shell commands on the user's system.
type Tool struct{}

func (t *Tool) Review(_ context.Context, toolCall *aipb.ToolCall) (*sgptpb.ToolCallMetadata, error) {
	execShellRequest, err := parseShellCommandArguments(toolCall)
	if err != nil {
		return nil, err
	}
	display := execShellRequest.GetCommand()
	if execShellRequest.GetWorkingDirectory() != "" {
		display = fmt.Sprintf("cd %s && %s", execShellRequest.GetWorkingDirectory(), execShellRequest.GetCommand())
	}
	// Shell commands are arbitrary code execution: never auto-execute.
	return &sgptpb.ToolCallMetadata{
		DisplayMessage: &sgptpb.DisplayMessage{Content: display},
	}, nil
}

func (t *Tool) Execute(_ context.Context, toolCall *aipb.ToolCall) (*aipb.ToolResult, error) {
	execShellRequest, err := parseShellCommandArguments(toolCall)
	if err != nil {
		return nil, err
	}
	command := exec.Command("sh", "-c", execShellRequest.GetCommand())
	if execShellRequest.GetWorkingDirectory() != "" {
		command.Dir = execShellRequest.GetWorkingDirectory()
	}
	output, err := command.CombinedOutput()
	execShellResponse := &sgptpb.ExecShellResponse{Output: string(output)}
	if err != nil {
		// Surface failures in the result so the model can react to them.
		execShellResponse.Error = err.Error()
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			execShellResponse.ExitCode = int32(exitError.ExitCode())
		}
	}
	return tool.NewStructuredToolResult(toolCall, execShellResponse)
}

var _ tool.Tool = (*Tool)(nil)

// RenderRequest renders the command as a shell fence instead of raw JSON.
func (t *Tool) RenderRequest(toolCall *aipb.ToolCall) (string, bool) {
	execShellRequest, err := parseShellCommandArguments(toolCall)
	if err != nil {
		return "", false
	}
	display := execShellRequest.GetCommand()
	if execShellRequest.GetWorkingDirectory() != "" {
		display = fmt.Sprintf("cd %s && %s", execShellRequest.GetWorkingDirectory(), execShellRequest.GetCommand())
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
