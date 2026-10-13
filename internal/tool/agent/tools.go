package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	aipb "github.com/malonaz/core/genproto/ai/v1"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/store"
	"github.com/malonaz/sgpt/internal/tool"
)

const (
	defaultWaitTimeout = 10 * time.Minute
	maxWaitTimeout     = time.Hour
)

// Tool definitions, built from the ToolService methods.
var (
	CreateAgents     = tool.MustBuildTool("create_agents", tool.HandlerIDCreateAgents, "sgpt.v1.ToolService.CreateAgents")
	MessageAgent     = tool.MustBuildTool("message_agent", tool.HandlerIDMessageAgent, "sgpt.v1.ToolService.MessageAgent")
	GetOperations    = tool.MustBuildTool("get_operations", tool.HandlerIDGetOperations, "sgpt.v1.ToolService.GetOperations")
	WaitOperations   = tool.MustBuildTool("wait_operations", tool.HandlerIDWaitOperations, "sgpt.v1.ToolService.WaitOperations")
	CancelOperations = tool.MustBuildTool("cancel_operations", tool.HandlerIDCancelOperations, "sgpt.v1.ToolService.CancelOperations")
)

func init() {
	tool.RegisterBuiltinGroup(GroupName, CreateAgents, MessageAgent, GetOperations, WaitOperations, CancelOperations)
}

func callerFrom(ctx context.Context) (tool.Caller, error) {
	caller, ok := tool.CallerFromContext(ctx)
	if !ok || caller.Agent == nil {
		return tool.Caller{}, fmt.Errorf("sub-agent tools called outside a session")
	}
	return caller, nil
}

// ---- create_agents ----

type createAgentsTool struct{ m *Manager }

// parse validates a request against what the calling session may delegate.
// Shared by Review and Execute so a rejected launch never reaches the user:
// the model gets the reason as the tool result and adjusts.
func (t *createAgentsTool) parse(ctx context.Context, toolCall *aipb.ToolCall) (*sgptpb.CreateAgentsRequest, tool.Caller, error) {
	request := &sgptpb.CreateAgentsRequest{}
	if err := tool.UnmarshalArguments(toolCall, request); err != nil {
		return nil, tool.Caller{}, err
	}
	caller, err := callerFrom(ctx)
	if err != nil {
		return nil, tool.Caller{}, err
	}
	if caller.Depth >= t.m.maxDepth {
		return nil, tool.Caller{}, fmt.Errorf("sub-agents may nest at most %d deep; this chat is at depth %d", t.m.maxDepth, caller.Depth)
	}
	if len(request.GetTasks()) == 0 {
		return nil, tool.Caller{}, fmt.Errorf("no tasks specified")
	}
	for i, task := range request.GetTasks() {
		if strings.TrimSpace(task.GetQuery()) == "" {
			return nil, tool.Caller{}, fmt.Errorf("task %d: no query specified", i+1)
		}
		if strings.TrimSpace(task.GetTitle()) == "" {
			return nil, tool.Caller{}, fmt.Errorf("task %d: no title specified", i+1)
		}
	}
	if err := validateTools(request.GetTools(), caller.Tools); err != nil {
		return nil, tool.Caller{}, err
	}
	return request, caller, nil
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

func (t *createAgentsTool) Review(ctx context.Context, toolCall *aipb.ToolCall) (*sgptpb.ToolCallMetadata, error) {
	request, caller, err := t.parse(ctx, toolCall)
	if err != nil {
		return nil, err
	}
	// Sub-agents spend tokens and may be granted mutating tools: never
	// auto-execute. Summarize the grant so the user reviews scope, not JSON.
	parts := []string{
		fmt.Sprintf("%d task(s)", len(request.GetTasks())),
		"tools: " + strings.Join(childTools(request.GetTools(), caller.Tools), ", "),
	}
	if len(request.GetFiles()) > 0 {
		parts = append(parts, "files: "+strings.Join(request.GetFiles(), ", "))
	}
	if request.GetModel() != "" {
		parts = append(parts, "model: "+request.GetModel())
	}
	return &sgptpb.ToolCallMetadata{
		DisplayMessage: &sgptpb.DisplayMessage{Content: strings.Join(parts, " | ")},
	}, nil
}

func (t *createAgentsTool) Execute(ctx context.Context, toolCall *aipb.ToolCall) (*aipb.ToolResult, error) {
	request, caller, err := t.parse(ctx, toolCall)
	if err != nil {
		return nil, err
	}
	// Launches outlive this call: they answer to the operations, not to the
	// turn that requested them.
	launchCtx := context.WithoutCancel(ctx)
	response := &sgptpb.CreateAgentsResponse{}
	t.m.mu.Lock()
	defer t.m.mu.Unlock()
	for _, task := range request.GetTasks() {
		op := t.m.newOperationLocked(t.m.newSubAgentLocked(caller.Agent, task.GetTitle()))
		opCtx, cancel := context.WithCancel(launchCtx)
		op.cancelLaunch = cancel
		go func() {
			defer cancel()
			t.m.launch(opCtx, op, launchRequest(request, task, caller), task.GetQuery())
		}()
		response.Operations = append(response.Operations, t.m.snapshotLocked(op))
	}
	return tool.NewStructuredToolResult(toolCall, response)
}

// launchRequest merges the batch-wide settings with one task's overrides.
func launchRequest(request *sgptpb.CreateAgentsRequest, task *sgptpb.AgentTask, caller tool.Caller) *LaunchRequest {
	model := task.GetModel()
	if model == "" {
		model = request.GetModel()
	}
	return &LaunchRequest{
		Caller:  caller,
		Title:   task.GetTitle(),
		Context: request.GetContext(),
		Files:   append(append([]string(nil), request.GetFiles()...), task.GetFiles()...),
		Tools:   childTools(request.GetTools(), caller.Tools),
		Model:   model,
	}
}

// RenderRequest renders the tasks as markdown instead of raw JSON. Tolerates
// partial arguments so the request is readable as it streams in.
func (t *createAgentsTool) RenderRequest(toolCall *aipb.ToolCall) (string, bool) {
	request := &sgptpb.CreateAgentsRequest{}
	if tool.UnmarshalArguments(toolCall, request) != nil || len(request.GetTasks()) == 0 {
		return "", false
	}
	var b strings.Builder
	if briefing := strings.TrimSpace(request.GetContext()); briefing != "" {
		b.WriteString(briefing)
		b.WriteString("\n\n")
	}
	for _, task := range request.GetTasks() {
		fmt.Fprintf(&b, "### %s\n\n%s\n\n", task.GetTitle(), task.GetQuery())
	}
	return strings.TrimSpace(b.String()), true
}

func (t *createAgentsTool) RenderResult(_ *aipb.ToolCall, toolResult *aipb.ToolResult) (string, bool) {
	response := &sgptpb.CreateAgentsResponse{}
	if tool.UnmarshalResult(toolResult, response) != nil {
		return "", false
	}
	return renderOperations(t.m.liveSnapshots(response.GetOperations())), true
}

func (t *createAgentsTool) RenderVersion(*aipb.ToolCall) string { return t.m.renderVersion() }

func (t *createAgentsTool) RenderHeader(toolCall *aipb.ToolCall) (string, bool) {
	// Tolerates partial arguments: falls back to the bare label until the
	// titles stream in.
	request := &sgptpb.CreateAgentsRequest{}
	if tool.UnmarshalArguments(toolCall, request) != nil || len(request.GetTasks()) == 0 {
		return agentIcon + " create agents", true
	}
	titles := make([]string, 0, len(request.GetTasks()))
	for _, task := range request.GetTasks() {
		if task.GetTitle() != "" {
			titles = append(titles, task.GetTitle())
		}
	}
	return agentIcon + " create agents: " + strings.Join(titles, ", "), true
}

// newSubAgentLocked registers a sub-agent. Caller holds the lock.
func (m *Manager) newSubAgentLocked(parent tool.Agent, title string) *subAgent {
	subAgent := &subAgent{name: fmt.Sprintf("agents/%d", len(m.agents)+1), title: title, parent: parent}
	m.agents[subAgent.name] = subAgent
	return subAgent
}

// ---- message_agent ----

type messageAgentTool struct{ m *Manager }

func (t *messageAgentTool) parse(ctx context.Context, toolCall *aipb.ToolCall) (*sgptpb.MessageAgentRequest, tool.Caller, error) {
	request := &sgptpb.MessageAgentRequest{}
	if err := tool.UnmarshalArguments(toolCall, request); err != nil {
		return nil, tool.Caller{}, err
	}
	caller, err := callerFrom(ctx)
	if err != nil {
		return nil, tool.Caller{}, err
	}
	if strings.TrimSpace(request.GetText()) == "" {
		return nil, tool.Caller{}, fmt.Errorf("no text specified")
	}
	return request, caller, nil
}

// recipient resolves who a message is for: the caller's launcher, or one of
// its own sub-agents.
func (t *messageAgentTool) recipient(caller tool.Caller, name string) (*subAgent, error) {
	t.m.mu.Lock()
	defer t.m.mu.Unlock()
	if name == parentAgent {
		self, ok := t.m.handleToAgent[caller.Agent]
		if !ok {
			return nil, fmt.Errorf("this chat has no launcher: it is not a sub-agent")
		}
		return self, nil
	}
	subAgent, ok := t.m.agents[name]
	if !ok || subAgent.parent != caller.Agent {
		return nil, fmt.Errorf("%s is not a sub-agent this chat launched", name)
	}
	if subAgent.handle == nil {
		return nil, fmt.Errorf("%s has not started yet; its launch operation is still queued", name)
	}
	return subAgent, nil
}

func (t *messageAgentTool) Review(ctx context.Context, toolCall *aipb.ToolCall) (*sgptpb.ToolCallMetadata, error) {
	request, caller, err := t.parse(ctx, toolCall)
	if err != nil {
		return nil, err
	}
	if _, err := t.recipient(caller, request.GetAgent()); err != nil {
		return nil, err
	}
	// The sub-agents were approved at launch; talking to them is within that
	// grant, and reviewing every exchange would defeat delegating at all.
	return &sgptpb.ToolCallMetadata{DisplayMessage: &sgptpb.DisplayMessage{}, AutoExecute: true}, nil
}

func (t *messageAgentTool) Execute(ctx context.Context, toolCall *aipb.ToolCall) (*aipb.ToolResult, error) {
	request, caller, err := t.parse(ctx, toolCall)
	if err != nil {
		return nil, err
	}
	recipient, err := t.recipient(caller, request.GetAgent())
	if err != nil {
		return nil, err
	}
	if request.GetAgent() == parentAgent {
		text := fmt.Sprintf("[message from sub-agent %s %q]\n\n%s", recipient.name, recipient.title, request.GetText())
		if _, err := recipient.parent.Deliver(store.NewAgentMessage(recipient.title, text)); err != nil {
			return nil, fmt.Errorf("messaging your launcher: %w", err)
		}
		return tool.NewStructuredToolResult(toolCall, &sgptpb.MessageAgentResponse{})
	}

	t.m.mu.Lock()
	op := t.m.newOperationLocked(recipient)
	op.launched = true
	t.m.mu.Unlock()
	outcomeCh, err := recipient.handle.Deliver(store.NewAgentMessage(launcherSender, "[message from your launcher]\n\n"+request.GetText()))
	if err != nil {
		t.m.complete(op, tool.TurnOutcome{Err: err})
		return nil, fmt.Errorf("messaging %s: %w", recipient.name, err)
	}
	go func() { t.m.complete(op, <-outcomeCh) }()
	t.m.mu.Lock()
	defer t.m.mu.Unlock()
	return tool.NewStructuredToolResult(toolCall, &sgptpb.MessageAgentResponse{Operation: t.m.snapshotLocked(op)})
}

func (t *messageAgentTool) RenderRequest(toolCall *aipb.ToolCall) (string, bool) {
	request := &sgptpb.MessageAgentRequest{}
	if tool.UnmarshalArguments(toolCall, request) != nil || request.GetText() == "" {
		return "", false
	}
	return request.GetText(), true
}

func (t *messageAgentTool) RenderResult(_ *aipb.ToolCall, toolResult *aipb.ToolResult) (string, bool) {
	response := &sgptpb.MessageAgentResponse{}
	if tool.UnmarshalResult(toolResult, response) != nil {
		return "", false
	}
	if response.GetOperation() == nil {
		return "delivered", true
	}
	return renderOperations(t.m.liveSnapshots([]*sgptpb.AgentOperation{response.GetOperation()})), true
}

func (t *messageAgentTool) RenderVersion(*aipb.ToolCall) string { return t.m.renderVersion() }

func (t *messageAgentTool) RenderHeader(toolCall *aipb.ToolCall) (string, bool) {
	request := &sgptpb.MessageAgentRequest{}
	if tool.UnmarshalArguments(toolCall, request) != nil || request.GetAgent() == "" {
		return agentIcon + " message agent", true
	}
	recipient := request.GetAgent()
	if recipient != parentAgent {
		recipient = t.m.title(recipient)
	}
	return agentIcon + " message → " + recipient, true
}

// title returns a sub-agent's title, or its name when unknown.
func (m *Manager) title(name string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if subAgent, ok := m.agents[name]; ok {
		return subAgent.title
	}
	return name
}

// ---- get_operations ----

type getOperationsTool struct{ m *Manager }

func (t *getOperationsTool) Review(ctx context.Context, toolCall *aipb.ToolCall) (*sgptpb.ToolCallMetadata, error) {
	if err := tool.UnmarshalArguments(toolCall, &sgptpb.GetOperationsRequest{}); err != nil {
		return nil, err
	}
	return &sgptpb.ToolCallMetadata{DisplayMessage: &sgptpb.DisplayMessage{}, AutoExecute: tool.NoSideEffects(toolCall)}, nil
}

func (t *getOperationsTool) Execute(ctx context.Context, toolCall *aipb.ToolCall) (*aipb.ToolResult, error) {
	request := &sgptpb.GetOperationsRequest{}
	if err := tool.UnmarshalArguments(toolCall, request); err != nil {
		return nil, err
	}
	caller, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	t.m.mu.Lock()
	defer t.m.mu.Unlock()
	ops, err := t.m.ownedOperationsLocked(caller.Agent, request.GetNames(), false)
	if err != nil {
		return nil, err
	}
	response := &sgptpb.GetOperationsResponse{}
	for _, op := range ops {
		response.Operations = append(response.Operations, t.m.snapshotLocked(op))
	}
	return tool.NewStructuredToolResult(toolCall, response)
}

func (t *getOperationsTool) RenderResult(_ *aipb.ToolCall, toolResult *aipb.ToolResult) (string, bool) {
	response := &sgptpb.GetOperationsResponse{}
	if tool.UnmarshalResult(toolResult, response) != nil {
		return "", false
	}
	return renderOperations(response.GetOperations()), true
}

func (t *getOperationsTool) RenderHeader(*aipb.ToolCall) (string, bool) {
	return agentIcon + " get operations", true
}

// ---- wait_operations ----

type waitOperationsTool struct{ m *Manager }

func (t *waitOperationsTool) Review(ctx context.Context, toolCall *aipb.ToolCall) (*sgptpb.ToolCallMetadata, error) {
	if err := tool.UnmarshalArguments(toolCall, &sgptpb.WaitOperationsRequest{}); err != nil {
		return nil, err
	}
	return &sgptpb.ToolCallMetadata{DisplayMessage: &sgptpb.DisplayMessage{}, AutoExecute: tool.NoSideEffects(toolCall)}, nil
}

func (t *waitOperationsTool) Execute(ctx context.Context, toolCall *aipb.ToolCall) (*aipb.ToolResult, error) {
	request := &sgptpb.WaitOperationsRequest{}
	if err := tool.UnmarshalArguments(toolCall, request); err != nil {
		return nil, err
	}
	caller, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	timeout := defaultWaitTimeout
	if seconds := request.GetTimeoutSeconds(); seconds > 0 {
		timeout = min(time.Duration(seconds)*time.Second, maxWaitTimeout)
	}
	reason, ops, err := t.m.wait(ctx, caller.Agent, request.GetNames(), request.GetReturnWhen() == sgptpb.WaitOperationsRequest_RETURN_WHEN_ANY, timeout)
	if err != nil {
		return nil, err
	}
	return tool.NewStructuredToolResult(toolCall, &sgptpb.WaitOperationsResponse{Reason: reason, Operations: ops})
}

// wait blocks until the operations finish (any or all of them), the timeout
// elapses, or a message is queued for caller — a sub-agent's question must
// reach a launcher blocked on that very sub-agent, or both would wait forever.
func (m *Manager) wait(ctx context.Context, caller tool.Agent, names []string, any bool, timeout time.Duration) (sgptpb.WaitOperationsResponse_Reason, []*sgptpb.AgentOperation, error) {
	m.mu.Lock()
	ops, err := m.ownedOperationsLocked(caller, names, true)
	if err != nil {
		m.mu.Unlock()
		return 0, nil, err
	}
	for _, op := range ops {
		op.watchers++
	}
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		for _, op := range ops {
			op.watchers--
		}
		m.mu.Unlock()
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	inbox := caller.Inbox()
	for {
		m.mu.Lock()
		finished := 0
		for _, op := range ops {
			if op.done {
				finished++
			}
		}
		changed := m.changed
		m.mu.Unlock()

		if finished == len(ops) || (any && finished > 0) {
			return sgptpb.WaitOperationsResponse_REASON_COMPLETED, m.snapshots(ops), nil
		}
		select {
		case <-changed:
		case <-inbox:
			return sgptpb.WaitOperationsResponse_REASON_MESSAGE, m.snapshots(ops), nil
		case <-timer.C:
			return sgptpb.WaitOperationsResponse_REASON_TIMEOUT, m.snapshots(ops), nil
		case <-ctx.Done():
			return 0, nil, ctx.Err()
		}
	}
}

func (m *Manager) snapshots(ops []*operation) []*sgptpb.AgentOperation {
	m.mu.Lock()
	defer m.mu.Unlock()
	snapshots := make([]*sgptpb.AgentOperation, 0, len(ops))
	for _, op := range ops {
		snapshots = append(snapshots, m.snapshotLocked(op))
	}
	return snapshots
}

func (t *waitOperationsTool) RenderResult(_ *aipb.ToolCall, toolResult *aipb.ToolResult) (string, bool) {
	response := &sgptpb.WaitOperationsResponse{}
	if tool.UnmarshalResult(toolResult, response) != nil {
		return "", false
	}
	var b strings.Builder
	switch response.GetReason() {
	case sgptpb.WaitOperationsResponse_REASON_TIMEOUT:
		b.WriteString("*timed out*\n\n")
	case sgptpb.WaitOperationsResponse_REASON_MESSAGE:
		b.WriteString("*interrupted by a message*\n\n")
	}
	b.WriteString(renderOperations(response.GetOperations()))
	return b.String(), true
}

func (t *waitOperationsTool) RenderHeader(*aipb.ToolCall) (string, bool) {
	return agentIcon + " wait for operations", true
}

// ---- cancel_operations ----

type cancelOperationsTool struct{ m *Manager }

func (t *cancelOperationsTool) Review(ctx context.Context, toolCall *aipb.ToolCall) (*sgptpb.ToolCallMetadata, error) {
	if err := tool.UnmarshalArguments(toolCall, &sgptpb.CancelOperationsRequest{}); err != nil {
		return nil, err
	}
	// Stopping delegated work only ever saves tokens.
	return &sgptpb.ToolCallMetadata{DisplayMessage: &sgptpb.DisplayMessage{}, AutoExecute: true}, nil
}

func (t *cancelOperationsTool) Execute(ctx context.Context, toolCall *aipb.ToolCall) (*aipb.ToolResult, error) {
	request := &sgptpb.CancelOperationsRequest{}
	if err := tool.UnmarshalArguments(toolCall, request); err != nil {
		return nil, err
	}
	caller, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	if len(request.GetNames()) == 0 {
		return nil, fmt.Errorf("no operations specified")
	}
	t.m.mu.Lock()
	ops, err := t.m.ownedOperationsLocked(caller.Agent, request.GetNames(), false)
	if err != nil {
		t.m.mu.Unlock()
		return nil, err
	}
	var handles []tool.Agent
	for _, op := range ops {
		if op.done {
			continue
		}
		op.cancelRequested = true
		if op.launched {
			handles = append(handles, op.subAgent.handle)
		} else {
			op.cancelLaunch()
		}
	}
	t.m.mu.Unlock()
	// Outside the lock: a session's cancellation calls back into the manager.
	for _, handle := range handles {
		handle.CancelTurn()
	}
	return tool.NewStructuredToolResult(toolCall, &sgptpb.CancelOperationsResponse{Operations: t.m.snapshots(ops)})
}

func (t *cancelOperationsTool) RenderResult(_ *aipb.ToolCall, toolResult *aipb.ToolResult) (string, bool) {
	response := &sgptpb.CancelOperationsResponse{}
	if tool.UnmarshalResult(toolResult, response) != nil {
		return "", false
	}
	return renderOperations(response.GetOperations()), true
}

func (t *cancelOperationsTool) RenderHeader(*aipb.ToolCall) (string, bool) {
	return agentIcon + " cancel operations", true
}

// ---- rendering ----

// agentIcon marks sub-agent activity; distinct from the model's 🤖.
const agentIcon = "👥"

// StateGlyph is the one-character state marker shared by the timeline and
// the TUI's agent views.
func StateGlyph(state sgptpb.AgentOperation_State) string {
	switch state {
	case sgptpb.AgentOperation_STATE_QUEUED:
		return "◌"
	case sgptpb.AgentOperation_STATE_RUNNING:
		return "●"
	case sgptpb.AgentOperation_STATE_AWAITING_REVIEW:
		return "▶"
	case sgptpb.AgentOperation_STATE_SUCCEEDED:
		return "✓"
	case sgptpb.AgentOperation_STATE_FAILED:
		return "✗"
	case sgptpb.AgentOperation_STATE_CANCELLED:
		return "⊘"
	}
	return "?"
}

// renderOperations renders operations as markdown: a state line each, with
// the report or error of finished ones below it.
func renderOperations(ops []*sgptpb.AgentOperation) string {
	var b strings.Builder
	for _, op := range ops {
		fmt.Fprintf(&b, "%s **%s** `%s`\n\n", StateGlyph(op.GetState()), op.GetTitle(), op.GetName())
		switch {
		case op.GetError() != "":
			fmt.Fprintf(&b, "**%s:** %s\n\n", strings.ToLower(strings.TrimPrefix(op.GetState().String(), "STATE_")), op.GetError())
		case op.GetResponse() != "":
			b.WriteString(op.GetResponse())
			b.WriteString("\n\n")
		}
	}
	return strings.TrimSpace(b.String())
}

var (
	_ tool.RequestRenderer = (*createAgentsTool)(nil)
	_ tool.ResultRenderer  = (*createAgentsTool)(nil)
	_ tool.HeaderRenderer  = (*createAgentsTool)(nil)
	_ tool.LiveRenderer    = (*createAgentsTool)(nil)
	_ tool.LiveRenderer    = (*messageAgentTool)(nil)
	_ tool.RequestRenderer = (*messageAgentTool)(nil)
	_ tool.ResultRenderer  = (*messageAgentTool)(nil)
	_ tool.HeaderRenderer  = (*messageAgentTool)(nil)
	_ tool.ResultRenderer  = (*getOperationsTool)(nil)
	_ tool.HeaderRenderer  = (*getOperationsTool)(nil)
	_ tool.ResultRenderer  = (*waitOperationsTool)(nil)
	_ tool.HeaderRenderer  = (*waitOperationsTool)(nil)
	_ tool.ResultRenderer  = (*cancelOperationsTool)(nil)
	_ tool.HeaderRenderer  = (*cancelOperationsTool)(nil)
)
