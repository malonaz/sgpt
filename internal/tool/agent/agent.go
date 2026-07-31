package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"

	aipb "github.com/malonaz/core/genproto/ai/v1"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/tool"
)

// Definition is the tool definition for launching sub-agents, built from
// the ToolService.Agent method.
var Definition = tool.MustBuildTool("agent", tool.HandlerIDAgent, "sgpt.v1.ToolService.Agent")

// LaunchRequest carries everything a CLI-launched chat can get.
type LaunchRequest struct {
	Query string
	Title string
	Files []string
	Tools []string
	Model string
}

// Launcher runs a sub-agent chat and blocks until it produces a final answer.
// Implemented by the TUI App (it owns tabs); injected late via SetLauncher
// because the registry is built before the App exists.
type Launcher interface {
	LaunchAgent(ctx context.Context, request *LaunchRequest) (string, error)
}

func parseArguments(toolCall *aipb.ToolCall) (*sgptpb.AgentRequest, error) {
	agentRequest := &sgptpb.AgentRequest{}
	if err := tool.UnmarshalArguments(toolCall, agentRequest); err != nil {
		return nil, err
	}
	if strings.TrimSpace(agentRequest.GetQuery()) == "" {
		return nil, fmt.Errorf("no query specified")
	}
	return agentRequest, nil
}

// Tool launches sub-agents in new chat tabs.
type Tool struct {
	mu       sync.Mutex
	launcher Launcher
}

func (t *Tool) SetLauncher(launcher Launcher) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.launcher = launcher
}

func (t *Tool) getLauncher() (Launcher, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.launcher == nil {
		return nil, fmt.Errorf("agent launcher not configured")
	}
	return t.launcher, nil
}

func (t *Tool) Review(_ context.Context, toolCall *aipb.ToolCall) (*sgptpb.ToolCallMetadata, error) {
	agentRequest, err := parseArguments(toolCall)
	if err != nil {
		return nil, err
	}
	// Sub-agents spend tokens and may be granted mutating tools: never
	// auto-execute. Summarize the grant so the user reviews scope, not JSON.
	var parts []string
	if len(agentRequest.GetTools()) > 0 {
		parts = append(parts, "tools: "+strings.Join(agentRequest.GetTools(), ", "))
	}
	if len(agentRequest.GetFiles()) > 0 {
		parts = append(parts, "files: "+strings.Join(agentRequest.GetFiles(), ", "))
	}
	if agentRequest.GetModel() != "" {
		parts = append(parts, "model: "+agentRequest.GetModel())
	}
	return &sgptpb.ToolCallMetadata{
		DisplayMessage: &sgptpb.DisplayMessage{Content: strings.Join(parts, " | ")},
	}, nil
}

func (t *Tool) Execute(ctx context.Context, toolCall *aipb.ToolCall) (*aipb.ToolResult, error) {
	agentRequest, err := parseArguments(toolCall)
	if err != nil {
		return nil, err
	}
	launcher, err := t.getLauncher()
	if err != nil {
		return nil, err
	}
	launchRequest := &LaunchRequest{
		Query: agentRequest.GetQuery(),
		Title: agentRequest.GetTitle(),
		Files: agentRequest.GetFiles(),
		Tools: agentRequest.GetTools(),
		Model: agentRequest.GetModel(),
	}
	// Blocks until the sub-agent's turn fully completes (including any tool
	// calls the user reviews in the sub-agent's tab).
	response, err := launcher.LaunchAgent(ctx, launchRequest)
	if err != nil {
		return nil, err
	}
	if response == "" {
		response = "sub-agent finished without a text response"
	}
	agentResponse := &sgptpb.AgentResponse{Response: response}
	return tool.NewStructuredToolResult(toolCall, agentResponse)
}

// RenderRequest renders the query as markdown instead of raw JSON. Tolerates
// partial arguments so the query is readable as it streams in.
func (t *Tool) RenderRequest(toolCall *aipb.ToolCall) (string, bool) {
	agentRequest := &sgptpb.AgentRequest{}
	if tool.UnmarshalArguments(toolCall, agentRequest) != nil || agentRequest.GetQuery() == "" {
		return "", false
	}
	return agentRequest.GetQuery(), true
}

func (t *Tool) RenderHeader(toolCall *aipb.ToolCall) (string, bool) {
	// Tolerates partial arguments: falls back to the bare label until the
	// title streams in.
	agentRequest := &sgptpb.AgentRequest{}
	if tool.UnmarshalArguments(toolCall, agentRequest) != nil || agentRequest.GetTitle() == "" {
		return "🤖 sub-agent", true
	}
	return "🤖 sub-agent: " + agentRequest.GetTitle(), true
}

var (
	_ tool.Tool            = (*Tool)(nil)
	_ tool.RequestRenderer = (*Tool)(nil)
	_ tool.HeaderRenderer  = (*Tool)(nil)
)

func init() { tool.RegisterBuiltin(Definition) }
