package shell

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"

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
	if _, err := parseShellCommandArguments(toolCall); err != nil {
		return nil, err
	}
	// Shell commands are arbitrary code execution: never auto-execute.
	// No display message: the row and the request fence show the command.
	return &sgptpb.ToolCallMetadata{DisplayMessage: &sgptpb.DisplayMessage{}}, nil
}

func (t *Tool) Execute(ctx context.Context, toolCall *aipb.ToolCall) (*aipb.ToolResult, error) {
	execShellRequest, err := parseShellCommandArguments(toolCall)
	if err != nil {
		return nil, err
	}
	command := exec.CommandContext(ctx, "sh", "-c", execShellRequest.GetCommand())
	if execShellRequest.GetWorkingDirectory() != "" {
		command.Dir = execShellRequest.GetWorkingDirectory()
	}
	// Own process group so cancellation kills the whole tree, not just `sh`;
	// WaitDelay stops orphaned grandchildren holding the pipe from blocking us.
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error { return syscall.Kill(-command.Process.Pid, syscall.SIGKILL) }
	command.WaitDelay = time.Second
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	// Only returned on a clean exit: a background child kept the pipe open.
	if errors.Is(err, exec.ErrWaitDelay) {
		err = nil
	}
	// Proto strings must be UTF-8: binary output would fail the whole result.
	execShellResponse := &sgptpb.ExecShellResponse{Output: strings.ToValidUTF8(string(output), "\uFFFD")}
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

// Summarize: the command is the subject, the exit code the outcome.
func (t *Tool) Summarize(toolCall *aipb.ToolCall, toolResult *aipb.ToolResult) (tool.Summary, bool) {
	summary := tool.Summary{Verb: "Shell", Subject: tool.Argument(toolCall, "command").GetStringValue()}
	if toolResult == nil || toolResult.GetError() != nil {
		return summary, true
	}
	exitCode := int(tool.ResultField(toolResult, "exit_code").GetNumberValue())
	errorText := tool.ResultField(toolResult, "error").GetStringValue()
	switch {
	case exitCode > 0:
		summary.Outcome, summary.Failed = fmt.Sprintf("exit %d", exitCode), true
	case errorText != "":
		// Failed without an exit code: killed by a signal, or never started.
		summary.Outcome, summary.Failed = errorText, true
	default:
		summary.Outcome = "exit 0"
	}
	return summary, true
}

// RenderRequest renders a command run elsewhere as a shell fence. A plain
// command declines: the row's subject already shows it.
func (t *Tool) RenderRequest(toolCall *aipb.ToolCall) (string, bool) {
	command := tool.Argument(toolCall, "command").GetStringValue()
	directory := tool.Argument(toolCall, "working_directory").GetStringValue()
	if command == "" || directory == "" {
		return "", false
	}
	return tool.Fence("sh", fmt.Sprintf("cd %s && %s", directory, command)), true
}

// RenderResult renders the output as the terminal showed it; the exit code
// lives in the summary's outcome.
func (t *Tool) RenderResult(_ *aipb.ToolCall, toolResult *aipb.ToolResult) (string, bool) {
	execShellResponse := &sgptpb.ExecShellResponse{}
	if tool.UnmarshalResult(toolResult, execShellResponse) != nil {
		return "", false
	}
	output := execShellResponse.GetOutput()
	if strings.TrimSpace(output) == "" {
		output = "(no output)"
	}
	return tool.Fence("text", output), true
}

var (
	_ tool.Summarizer      = (*Tool)(nil)
	_ tool.RequestRenderer = (*Tool)(nil)
	_ tool.ResultRenderer  = (*Tool)(nil)
)

func init() { tool.RegisterBuiltin(Definition) }
