package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	aipb "github.com/malonaz/core/genproto/ai/v1"
	jsonpb "github.com/malonaz/core/genproto/json/v1"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/tool"
)

// Definition is the tool definition for launching sub-agents.
var Definition = &aipb.Tool{
	Name:        "agent",
	Description: "Launch a sub-agent in a new chat tab to work on a self-contained task. The sub-agent receives the query, optional injected files and tools, runs until it produces a final answer, and that answer is returned as this tool's result. Provide all necessary context in the query: the sub-agent shares none of this conversation.",
	JsonSchema: &jsonpb.Schema{
		Type: "object",
		Properties: map[string]*jsonpb.Schema{
			"query": {Type: "string", Description: "Task for the sub-agent, including all required context"},
			"files": {
				Type:        "array",
				Description: "File paths to inject into the sub-agent's context",
				Items:       &jsonpb.Schema{Type: "string"},
			},
			"tools": {
				Type:        "array",
				Description: "Tools to grant the sub-agent (built-in tools or configured tool engines)",
				Items:       &jsonpb.Schema{Type: "string"},
			},
			"model": {Type: "string", Description: "Optional model override for the sub-agent"},
		},
		Required: []string{"query"},
	},
	Annotations: map[string]string{
		tool.ToolHandlerIDAnnotation: tool.HandlerIDAgent,
	},
}

// LaunchRequest carries everything a CLI-launched chat can get.
type LaunchRequest struct {
	Query string
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

type arguments struct {
	Query string   `json:"query"`
	Files []string `json:"files"`
	Tools []string `json:"tools"`
	Model string   `json:"model"`
}

func parseArguments(toolCall *aipb.ToolCall) (*arguments, error) {
	bytes, err := tool.ArgumentsJSON(toolCall)
	if err != nil {
		return nil, err
	}
	arguments := &arguments{}
	if err := json.Unmarshal(bytes, arguments); err != nil {
		return nil, fmt.Errorf("parsing tool arguments: %w", err)
	}
	if strings.TrimSpace(arguments.Query) == "" {
		return nil, fmt.Errorf("no query specified")
	}
	return arguments, nil
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
	arguments, err := parseArguments(toolCall)
	if err != nil {
		return nil, err
	}
	// Sub-agents spend tokens and may be granted mutating tools: never
	// auto-execute. Summarize the grant so the user reviews scope, not JSON.
	var parts []string
	if len(arguments.Tools) > 0 {
		parts = append(parts, "tools: "+strings.Join(arguments.Tools, ", "))
	}
	if len(arguments.Files) > 0 {
		parts = append(parts, "files: "+strings.Join(arguments.Files, ", "))
	}
	if arguments.Model != "" {
		parts = append(parts, "model: "+arguments.Model)
	}
	return &sgptpb.ToolCallMetadata{
		DisplayMessage: &sgptpb.DisplayMessage{Content: strings.Join(parts, " | ")},
	}, nil
}

func (t *Tool) Execute(ctx context.Context, toolCall *aipb.ToolCall) (*aipb.ToolResult, error) {
	arguments, err := parseArguments(toolCall)
	if err != nil {
		return nil, err
	}
	launcher, err := t.getLauncher()
	if err != nil {
		return nil, err
	}
	launchRequest := &LaunchRequest{
		Query: arguments.Query,
		Files: arguments.Files,
		Tools: arguments.Tools,
		Model: arguments.Model,
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
	return &aipb.ToolResult{
		ToolName:   toolCall.Name,
		ToolCallId: toolCall.Id,
		Result:     &aipb.ToolResult_Content{Content: response},
	}, nil
}

// RenderRequest renders the query as markdown instead of raw JSON. Tolerates
// partial arguments so the query is readable as it streams in.
func (t *Tool) RenderRequest(toolCall *aipb.ToolCall) (string, bool) {
	bytes, err := tool.ArgumentsJSON(toolCall)
	if err != nil {
		return "", false
	}
	arguments := &arguments{}
	if json.Unmarshal(bytes, arguments) != nil || arguments.Query == "" {
		return "", false
	}
	return arguments.Query, true
}

func (t *Tool) RenderHeader(*aipb.ToolCall) (string, bool) {
	return "🤖 sub-agent", true
}

var (
	_ tool.Tool            = (*Tool)(nil)
	_ tool.RequestRenderer = (*Tool)(nil)
	_ tool.HeaderRenderer  = (*Tool)(nil)
)

func init() { tool.RegisterBuiltin(Definition) }
