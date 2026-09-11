// Package agent implements the `agent` tool: fanning a batch of tasks out to
// sub-agent chats and returning their final answers to the launcher.
package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	aipb "github.com/malonaz/core/genproto/ai/v1"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/permission"
	"github.com/malonaz/sgpt/internal/tool"
)

const (
	defaultMaxConcurrent = 4
	defaultMaxDepth      = 2
)

// Definition is the tool definition for launching sub-agents, built from
// the ToolService.Agent method.
var Definition = tool.MustBuildTool("agent", tool.HandlerIDAgent, "sgpt.v1.ToolService.Agent")

// LaunchRequest is one sub-agent to run: the task, its context and exactly
// what the launching session hands down.
type LaunchRequest struct {
	Caller  tool.Caller
	Title   string
	Query   string
	Context string
	Files   []string
	Tools   []string
	Model   string
	// Permissions narrow the caller's policy for this sub-agent.
	Permissions []*sgptpb.PermissionRule
}

// LaunchResult is a sub-agent's outcome.
type LaunchResult struct {
	// Chat is the sub-agent's chat resource name.
	Chat string
	// Response is the sub-agent's final message.
	Response string
}

// Launcher runs a sub-agent chat and blocks until it produces a final answer.
// Implemented by the TUI App (it owns tabs); injected late via SetLauncher
// because the registry is built before the App exists.
type Launcher interface {
	LaunchAgent(ctx context.Context, request *LaunchRequest) (*LaunchResult, error)
}

// Tool launches sub-agents in new chat tabs. One instance serves every
// session, so the concurrency cap is TUI-wide.
type Tool struct {
	maxDepth int
	// slots bounds the sub-agents running at once; a batch beyond it queues.
	slots chan struct{}

	mu       sync.Mutex
	launcher Launcher
}

// NewTool builds the agent tool from configuration (nil for defaults).
func NewTool(configuration *sgptpb.AgentConfiguration) *Tool {
	maxConcurrent := int(configuration.GetMaxConcurrent())
	if maxConcurrent <= 0 {
		maxConcurrent = defaultMaxConcurrent
	}
	maxDepth := int(configuration.GetMaxDepth())
	if maxDepth <= 0 {
		maxDepth = defaultMaxDepth
	}
	return &Tool{maxDepth: maxDepth, slots: make(chan struct{}, maxConcurrent)}
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

// parse validates a request against what the calling session may delegate.
// Shared by Review and Execute so a rejected launch never reaches the user:
// the model gets the reason as the tool result and adjusts.
func (t *Tool) parse(ctx context.Context, toolCall *aipb.ToolCall) (*sgptpb.AgentRequest, tool.Caller, error) {
	agentRequest := &sgptpb.AgentRequest{}
	if err := tool.UnmarshalArguments(toolCall, agentRequest); err != nil {
		return nil, tool.Caller{}, err
	}
	caller, ok := tool.CallerFromContext(ctx)
	if !ok {
		return nil, tool.Caller{}, fmt.Errorf("agent tool called outside a session")
	}
	if caller.Depth >= t.maxDepth {
		return nil, tool.Caller{}, fmt.Errorf("sub-agents may nest at most %d deep; this chat is at depth %d", t.maxDepth, caller.Depth)
	}
	if len(agentRequest.GetTasks()) == 0 {
		return nil, tool.Caller{}, fmt.Errorf("no tasks specified")
	}
	for i, task := range agentRequest.GetTasks() {
		if strings.TrimSpace(task.GetQuery()) == "" {
			return nil, tool.Caller{}, fmt.Errorf("task %d: no query specified", i+1)
		}
		if strings.TrimSpace(task.GetTitle()) == "" {
			return nil, tool.Caller{}, fmt.Errorf("task %d: no title specified", i+1)
		}
	}
	if err := validateTools(agentRequest.GetTools(), caller.Tools); err != nil {
		return nil, tool.Caller{}, err
	}
	for i, rule := range agentRequest.GetPermissions() {
		// Compiled again in the child policy; failing here keeps the review
		// honest about what will run.
		if _, err := caller.Policy.Child([]*sgptpb.PermissionRule{rule}); err != nil {
			return nil, tool.Caller{}, err
		}
		if rule.GetMode() == permission.ModeAllow {
			return nil, tool.Caller{}, fmt.Errorf("permission rule %d (%s): sub-agents inherit this chat's permissions and rules may only tighten them (review or deny)", i+1, rule.GetTool())
		}
	}
	return agentRequest, caller, nil
}

// validateTools enforces the grant ceiling: a sub-agent never gets a tool
// its launcher lacks.
func validateTools(requested, available []string) error {
	availableSet := make(map[string]bool, len(available))
	for _, name := range available {
		availableSet[name] = true
	}
	var unavailable []string
	for _, name := range requested {
		if !availableSet[name] {
			unavailable = append(unavailable, name)
		}
	}
	if len(unavailable) == 0 {
		return nil
	}
	sort.Strings(unavailable)
	return fmt.Errorf("tools not available to this chat: %s (available: %s)",
		strings.Join(unavailable, ", "), strings.Join(available, ", "))
}

func (t *Tool) Review(ctx context.Context, toolCall *aipb.ToolCall) (*sgptpb.ToolCallMetadata, error) {
	agentRequest, caller, err := t.parse(ctx, toolCall)
	if err != nil {
		return nil, err
	}
	// Sub-agents spend tokens and may be granted mutating tools: never
	// auto-execute. Summarize the grant so the user reviews scope, not JSON.
	tools := "none"
	if granted := t.launchRequest(agentRequest, agentRequest.GetTasks()[0], caller).Tools; len(granted) > 0 {
		tools = strings.Join(granted, ", ")
	}
	parts := []string{
		fmt.Sprintf("%d task(s)", len(agentRequest.GetTasks())),
		"tools: " + tools,
	}
	if len(agentRequest.GetFiles()) > 0 {
		parts = append(parts, "files: "+strings.Join(agentRequest.GetFiles(), ", "))
	}
	if agentRequest.GetModel() != "" {
		parts = append(parts, "model: "+agentRequest.GetModel())
	}
	if rules := agentRequest.GetPermissions(); len(rules) > 0 {
		described := make([]string, len(rules))
		for i, rule := range rules {
			described[i] = permission.Describe(rule)
		}
		parts = append(parts, "permissions: "+strings.Join(described, "; "))
	}
	return &sgptpb.ToolCallMetadata{
		DisplayMessage: &sgptpb.DisplayMessage{Content: strings.Join(parts, " | ")},
	}, nil
}

func (t *Tool) Execute(ctx context.Context, toolCall *aipb.ToolCall) (*aipb.ToolResult, error) {
	agentRequest, caller, err := t.parse(ctx, toolCall)
	if err != nil {
		return nil, err
	}
	launcher, err := t.getLauncher()
	if err != nil {
		return nil, err
	}

	// Every task runs concurrently (bounded by slots) and every outcome is
	// reported: one failed sub-agent must not discard the others' work.
	results := make([]*sgptpb.AgentResponse_Result, len(agentRequest.GetTasks()))
	var wg sync.WaitGroup
	for i, task := range agentRequest.GetTasks() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = &sgptpb.AgentResponse_Result{Title: task.GetTitle()}
			launchResult, err := t.launch(ctx, launcher, t.launchRequest(agentRequest, task, caller))
			if err != nil {
				results[i].Error = err.Error()
				return
			}
			results[i].Chat = launchResult.Chat
			results[i].Response = launchResult.Response
		}()
	}
	wg.Wait()
	return tool.NewStructuredToolResult(toolCall, &sgptpb.AgentResponse{Results: results})
}

// launchRequest merges the batch-wide settings with one task's overrides.
func (t *Tool) launchRequest(agentRequest *sgptpb.AgentRequest, task *sgptpb.AgentTask, caller tool.Caller) *LaunchRequest {
	tools := agentRequest.GetTools()
	if len(tools) == 0 {
		tools = caller.Tools
	}
	model := task.GetModel()
	if model == "" {
		model = agentRequest.GetModel()
	}
	return &LaunchRequest{
		Caller:      caller,
		Title:       task.GetTitle(),
		Query:       task.GetQuery(),
		Context:     agentRequest.GetContext(),
		Files:       append(append([]string(nil), agentRequest.GetFiles()...), task.GetFiles()...),
		Tools:       tools,
		Model:       model,
		Permissions: agentRequest.GetPermissions(),
	}
}

// launch waits for a slot, then runs the sub-agent to completion.
func (t *Tool) launch(ctx context.Context, launcher Launcher, request *LaunchRequest) (*LaunchResult, error) {
	select {
	case t.slots <- struct{}{}:
		defer func() { <-t.slots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	launchResult, err := launcher.LaunchAgent(ctx, request)
	if err != nil {
		return nil, err
	}
	if launchResult.Response == "" {
		return nil, fmt.Errorf("sub-agent finished without a text response")
	}
	return launchResult, nil
}

// SystemPrompt composes a sub-agent's system prompt: the role's prompt, the
// working contract every sub-agent follows, and the launcher's briefing.
func SystemPrompt(rolePrompt, briefing string) string {
	var b strings.Builder
	if rolePrompt != "" {
		b.WriteString(rolePrompt)
		b.WriteString("\n\n")
	}
	b.WriteString(contract)
	if briefing = strings.TrimSpace(briefing); briefing != "" {
		b.WriteString("\n\n# Briefing\n\n")
		b.WriteString(briefing)
	}
	return b.String()
}

// contract is what makes a chat a sub-agent: it is spoken to by a program,
// not a person, and its last message is the deliverable.
const contract = `# Sub-agent

You are a sub-agent: another chat launched you with one task and is waiting
for your answer. Nobody reads your messages as you go and nobody will answer
a question, so work the task to completion on your own — make reasonable
assumptions and state them. Your final message is returned verbatim to the
launcher as your whole report: make it self-contained, specific (paths,
names, line numbers) and free of preamble.`

// RenderRequest renders the tasks as markdown instead of raw JSON. Tolerates
// partial arguments so the request is readable as it streams in.
func (t *Tool) RenderRequest(toolCall *aipb.ToolCall) (string, bool) {
	agentRequest := &sgptpb.AgentRequest{}
	if tool.UnmarshalArguments(toolCall, agentRequest) != nil || len(agentRequest.GetTasks()) == 0 {
		return "", false
	}
	var b strings.Builder
	if briefing := strings.TrimSpace(agentRequest.GetContext()); briefing != "" {
		b.WriteString(briefing)
		b.WriteString("\n\n")
	}
	for _, task := range agentRequest.GetTasks() {
		fmt.Fprintf(&b, "### %s\n\n%s\n\n", task.GetTitle(), task.GetQuery())
	}
	return strings.TrimSpace(b.String()), true
}

// RenderResult renders each sub-agent's report under its title.
func (t *Tool) RenderResult(toolCall *aipb.ToolCall, toolResult *aipb.ToolResult) (string, bool) {
	agentResponse := &sgptpb.AgentResponse{}
	if err := tool.UnmarshalResult(toolResult, agentResponse); err != nil || len(agentResponse.GetResults()) == 0 {
		return "", false
	}
	var b strings.Builder
	for _, result := range agentResponse.GetResults() {
		fmt.Fprintf(&b, "### %s\n\n", result.GetTitle())
		if result.GetError() != "" {
			fmt.Fprintf(&b, "**failed:** %s\n\n", result.GetError())
			continue
		}
		b.WriteString(result.GetResponse())
		b.WriteString("\n\n")
	}
	return strings.TrimSpace(b.String()), true
}

func (t *Tool) RenderHeader(toolCall *aipb.ToolCall) (string, bool) {
	// Tolerates partial arguments: falls back to the bare label until the
	// titles stream in.
	agentRequest := &sgptpb.AgentRequest{}
	if tool.UnmarshalArguments(toolCall, agentRequest) != nil || len(agentRequest.GetTasks()) == 0 {
		return "🤖 sub-agents", true
	}
	titles := make([]string, 0, len(agentRequest.GetTasks()))
	for _, task := range agentRequest.GetTasks() {
		if task.GetTitle() != "" {
			titles = append(titles, task.GetTitle())
		}
	}
	return "🤖 sub-agents: " + strings.Join(titles, ", "), true
}

var (
	_ tool.Tool            = (*Tool)(nil)
	_ tool.RequestRenderer = (*Tool)(nil)
	_ tool.ResultRenderer  = (*Tool)(nil)
	_ tool.HeaderRenderer  = (*Tool)(nil)
)

func init() { tool.RegisterBuiltin(Definition) }
