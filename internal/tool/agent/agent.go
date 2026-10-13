// Package agent implements the sub-agent tools: launching sub-agent chats,
// messaging them (and a sub-agent its launcher) and following their turns as
// operations — asynchronously, so a launcher keeps working, polls or blocks
// as it sees fit.
package agent

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"sync"

	aipb "github.com/malonaz/core/genproto/ai/v1"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/store"
	"github.com/malonaz/sgpt/internal/tool"
)

const (
	defaultMaxConcurrent = 4
	defaultMaxDepth      = 2

	// GroupName is the user-facing name enabling every sub-agent tool.
	GroupName = "agent"
	// parentAgent addresses a sub-agent's launcher in message_agent.
	parentAgent = "parent"
	// launcherSender labels, in a sub-agent's chat, what its launcher sent.
	launcherSender = "launcher"
)

var errNoLauncher = errors.New("sub-agent launching is not configured")

// LaunchRequest is one sub-agent to open: what the launching session hands
// down, narrowed by the request. Its task arrives as the first message.
type LaunchRequest struct {
	Caller tool.Caller
	// Name is the sub-agent's, recorded on its chat.
	Name string
	// Chat, when set, is a dormant sub-agent's chat to reopen, history and
	// all, instead of creating one.
	Chat    *aipb.Chat
	Title   string
	Context string
	Files   []string
	Tools   []string
	Model   string
	// ReasoningEffort is already resolved: the override or the caller's.
	ReasoningEffort aipb.ReasoningEffort
}

// Launcher opens a sub-agent chat — new, or a dormant one reopened — ready to
// be messaged. Implemented by the TUI App (it owns tabs); injected late via
// SetLauncher because the registry is built before the App exists.
type Launcher interface {
	LaunchAgent(ctx context.Context, request *LaunchRequest) (tool.Agent, error)
}

// Manager owns the sub-agents and their operations. One instance serves every
// session, so the concurrency cap is TUI-wide.
type Manager struct {
	maxDepth int
	// slots bounds the sub-agents running their first turn at once per
	// nesting level; a batch beyond it queues. Per level, since a sub-agent
	// blocking on its own sub-agents' slots while holding an ancestor's
	// would deadlock.
	slots []chan struct{}
	chats ChatLister

	mu       sync.Mutex
	launcher Launcher
	// agents are every sub-agent, oldest first.
	agents      []*subAgent
	nameToAgent map[string]*subAgent
	// handleToAgent finds the sub-agent a session is, to reach its launcher.
	handleToAgent map[tool.Agent]*subAgent
	// operations are every operation started, oldest first.
	operations      []*operation
	nameToOperation map[string]*operation
	// changed is closed, and replaced, on every operation state change;
	// revision counts those changes.
	changed  chan struct{}
	revision uint64
}

type subAgent struct {
	name  string
	title string
	// parent is the session that launched it, or its dormant stand-in.
	parent tool.Agent
	// handle is the sub-agent's session: nil until its launch completes, a
	// dormant stand-in while restored but not loaded.
	handle tool.Agent
	// latest is its latest operation; nil when restored and not messaged since.
	latest *operation
	// restoredState is the last known state of a restored sub-agent.
	restoredState sgptpb.AgentOperation_State
}

// operation is one sub-agent turn requested by its launcher.
type operation struct {
	name     string
	subAgent *subAgent
	launched bool
	done     bool
	outcome  tool.TurnOutcome
	// cancelRequested marks a cancellation the launcher (or the user, through
	// it) asked for: reporting it back would be noise, or wake a chat the
	// user just stopped.
	cancelRequested bool
	// watchers counts the waits blocked on the operation: one of them takes
	// the result, so it is not also reported as a message.
	watchers int
	// cancelLaunch aborts a launch still queued for a slot.
	cancelLaunch context.CancelFunc
}

// NewManager builds the manager from configuration (nil for defaults); chats
// finds the sub-agents to restore.
func NewManager(configuration *sgptpb.AgentConfiguration, chats ChatLister) *Manager {
	maxConcurrent := int(configuration.GetMaxConcurrent())
	if maxConcurrent <= 0 {
		maxConcurrent = defaultMaxConcurrent
	}
	maxDepth := int(configuration.GetMaxDepth())
	if maxDepth <= 0 {
		maxDepth = defaultMaxDepth
	}
	slots := make([]chan struct{}, maxDepth)
	for i := range slots {
		slots[i] = make(chan struct{}, maxConcurrent)
	}
	return &Manager{
		maxDepth:        maxDepth,
		slots:           slots,
		chats:           chats,
		nameToAgent:     map[string]*subAgent{},
		handleToAgent:   map[tool.Agent]*subAgent{},
		nameToOperation: map[string]*operation{},
		changed:         make(chan struct{}),
	}
}

func (m *Manager) SetLauncher(launcher Launcher) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.launcher = launcher
}

// Tools returns the sub-agent tools by handler ID, for registration.
func (m *Manager) Tools() map[string]tool.Tool {
	return map[string]tool.Tool{
		tool.HandlerIDCreateAgents:     &createAgentsTool{m},
		tool.HandlerIDMessageAgent:     &messageAgentTool{m},
		tool.HandlerIDGetOperations:    &getOperationsTool{m},
		tool.HandlerIDWaitOperations:   &waitOperationsTool{m},
		tool.HandlerIDCancelOperations: &cancelOperationsTool{m},
	}
}

// newName returns a name in collection that taken lacks. Random rather than
// counted: a transcript's names from an earlier run must not collide with
// this run's.
func newName[V any](collection string, taken map[string]V) string {
	for {
		name := fmt.Sprintf("%s/%08x", collection, rand.Uint32())
		if _, ok := taken[name]; !ok {
			return name
		}
	}
}

// addAgentLocked registers a sub-agent. Caller holds the lock.
func (m *Manager) addAgentLocked(subAgent *subAgent) {
	m.agents = append(m.agents, subAgent)
	m.nameToAgent[subAgent.name] = subAgent
}

// bindLocked makes handle the sub-agent's session, moving what it launched
// over from the handle it replaces. Caller holds the lock.
func (m *Manager) bindLocked(subAgent *subAgent, handle tool.Agent) {
	if previous := subAgent.handle; previous != nil {
		delete(m.handleToAgent, previous)
		for _, child := range m.agents {
			if child.parent == previous {
				child.parent = handle
			}
		}
	}
	subAgent.handle = handle
	m.handleToAgent[handle] = subAgent
}

func (m *Manager) newOperationLocked(subAgent *subAgent) *operation {
	op := &operation{name: newName("operations", m.nameToOperation), subAgent: subAgent}
	m.operations = append(m.operations, op)
	m.nameToOperation[op.name] = op
	subAgent.latest = op
	m.broadcastLocked()
	return op
}

func (m *Manager) broadcastLocked() {
	close(m.changed)
	m.changed = make(chan struct{})
	m.revision++
}

// liveSnapshots refreshes recorded operation snapshots with their current
// state, so a rendered result follows the operations it started.
func (m *Manager) liveSnapshots(recorded []*sgptpb.AgentOperation) []*sgptpb.AgentOperation {
	m.mu.Lock()
	defer m.mu.Unlock()
	live := make([]*sgptpb.AgentOperation, 0, len(recorded))
	for _, snapshot := range recorded {
		live = append(live, m.liveSnapshotLocked(snapshot))
	}
	return live
}

// liveSnapshotLocked refreshes one recorded snapshot. An operation of an
// earlier run keeps a final state, which can no longer change; otherwise it
// was cut off or ended unseen, and takes its sub-agent's last known state.
// Caller holds the lock.
func (m *Manager) liveSnapshotLocked(recorded *sgptpb.AgentOperation) *sgptpb.AgentOperation {
	if op, ok := m.nameToOperation[recorded.GetName()]; ok {
		return m.snapshotLocked(op)
	}
	if isFinal(recorded.GetState()) {
		return recorded
	}
	snapshot := proto.CloneOf(recorded)
	snapshot.State = sgptpb.AgentOperation_STATE_UNSPECIFIED
	if subAgent, ok := m.nameToAgent[recorded.GetAgent()]; ok {
		snapshot.State = agentStateLocked(subAgent)
	}
	return snapshot
}

func (m *Manager) renderVersion() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	version := fmt.Sprint(m.revision)
	// Entering or leaving review doesn't broadcast, yet changes rendered states.
	for i, op := range m.operations {
		if op.launched && !op.done && op.subAgent.handle.AwaitingReview() {
			version += fmt.Sprintf(".%d", i)
		}
	}
	return version
}

// launch waits for a slot, opens the sub-agent and runs its first turn on
// the task, holding the slot until that turn ends.
func (m *Manager) launch(ctx context.Context, op *operation, request *LaunchRequest, query string) {
	slots := m.slots[request.Caller.Depth]
	select {
	case slots <- struct{}{}:
		defer func() { <-slots }()
	case <-ctx.Done():
		m.complete(op, tool.TurnOutcome{Err: ctx.Err()})
		return
	}
	m.mu.Lock()
	launcher := m.launcher
	m.mu.Unlock()
	if launcher == nil {
		m.complete(op, tool.TurnOutcome{Err: errNoLauncher})
		return
	}
	handle, err := launcher.LaunchAgent(ctx, request)
	if err == nil {
		// Cancelled while the tab opened: it stays, idle.
		err = ctx.Err()
	}
	if err != nil {
		m.complete(op, tool.TurnOutcome{Err: err})
		return
	}
	m.mu.Lock()
	m.bindLocked(op.subAgent, handle)
	op.launched = true
	m.broadcastLocked()
	m.mu.Unlock()
	m.run(op, handle, store.NewAgentMessage(launcherSender, query))
}

// run delivers a message to a sub-agent and resolves op with the turn that
// answers it. Blocking.
func (m *Manager) run(op *operation, handle tool.Agent, message *aipb.Message) {
	outcomeCh, err := handle.Deliver(message)
	if err != nil {
		m.complete(op, tool.TurnOutcome{Err: err})
		return
	}
	m.complete(op, <-outcomeCh)
}

// complete resolves an operation and, unless a wait takes the result or the
// launcher asked for the cancellation, reports it to the launcher as a
// message — waking it if idle, which is what makes sub-agents asynchronous.
func (m *Manager) complete(op *operation, outcome tool.TurnOutcome) {
	if outcome.Err == nil && outcome.Text == "" {
		outcome.Err = errors.New("the sub-agent finished without a text response")
	}
	m.mu.Lock()
	if op.done {
		m.mu.Unlock()
		return
	}
	op.done = true
	op.outcome = outcome
	report := op.watchers == 0 && !op.cancelRequested
	parent := op.subAgent.parent
	m.broadcastLocked()
	m.mu.Unlock()
	if report {
		// A closed launcher has nobody to tell; a launcher out of agent
		// turns keeps the report queued and alerts the user itself.
		parent.Deliver(store.NewAgentMessage(op.subAgent.title, reportText(op)))
	}
}

// reportText is how a finished operation reads to the launcher.
func reportText(op *operation) string {
	who := fmt.Sprintf("sub-agent %s %q", op.subAgent.name, op.subAgent.title)
	switch err := op.outcome.Err; {
	case err == nil:
		return fmt.Sprintf("[%s finished: report from %s]\n\n%s", op.name, who, op.outcome.Text)
	case isCancellation(err):
		return fmt.Sprintf("[%s cancelled: the user stopped %s]", op.name, who)
	default:
		return fmt.Sprintf("[%s failed: %s produced no answer: %v]", op.name, who, err)
	}
}

func isCancellation(err error) bool {
	return errors.Is(err, context.Canceled) || grpcstatus.Code(err) == codes.Canceled
}

// snapshotLocked renders an operation for the model. Caller holds the lock.
func (m *Manager) snapshotLocked(op *operation) *sgptpb.AgentOperation {
	snapshot := &sgptpb.AgentOperation{
		Name:  op.name,
		Agent: op.subAgent.name,
		Title: op.subAgent.title,
		State: stateLocked(op),
	}
	if op.done {
		if op.outcome.Err != nil {
			snapshot.Error = op.outcome.Err.Error()
		} else {
			snapshot.Response = op.outcome.Text
		}
	}
	return snapshot
}

// agentStateLocked is the state of the sub-agent's latest operation, or its
// last known one while restored. Caller holds the lock.
func agentStateLocked(subAgent *subAgent) sgptpb.AgentOperation_State {
	if subAgent.latest != nil {
		return stateLocked(subAgent.latest)
	}
	return subAgent.restoredState
}

func isFinal(state sgptpb.AgentOperation_State) bool {
	switch state {
	case sgptpb.AgentOperation_STATE_SUCCEEDED, sgptpb.AgentOperation_STATE_FAILED, sgptpb.AgentOperation_STATE_CANCELLED:
		return true
	}
	return false
}

func stateLocked(op *operation) sgptpb.AgentOperation_State {
	switch {
	case op.done && op.outcome.Err == nil:
		return sgptpb.AgentOperation_STATE_SUCCEEDED
	case op.done && isCancellation(op.outcome.Err):
		return sgptpb.AgentOperation_STATE_CANCELLED
	case op.done:
		return sgptpb.AgentOperation_STATE_FAILED
	case !op.launched:
		return sgptpb.AgentOperation_STATE_QUEUED
	case op.subAgent.handle.AwaitingReview():
		return sgptpb.AgentOperation_STATE_AWAITING_REVIEW
	default:
		return sgptpb.AgentOperation_STATE_RUNNING
	}
}

// ownedOperations resolves operation names against those caller started;
// empty names select them all, oldest first, or only the unfinished ones.
// Caller holds the lock.
func (m *Manager) ownedOperationsLocked(caller tool.Agent, names []string, unfinishedOnly bool) ([]*operation, error) {
	if len(names) == 0 {
		var owned []*operation
		for _, op := range m.operations {
			if op.subAgent.parent == caller && !(unfinishedOnly && op.done) {
				owned = append(owned, op)
			}
		}
		return owned, nil
	}
	owned := make([]*operation, 0, len(names))
	for _, name := range names {
		op, ok := m.nameToOperation[name]
		if !ok || op.subAgent.parent != caller {
			return nil, fmt.Errorf("%s is not an operation this chat started", name)
		}
		owned = append(owned, op)
	}
	return owned, nil
}

// CancelDescendants implements tool.Supervisor: the sub-agents directly
// below agent stop their turns — and, through their own sessions, theirs.
func (m *Manager) CancelDescendants(agent tool.Agent) {
	m.mu.Lock()
	var handles []tool.Agent
	for _, op := range m.operations {
		if op.subAgent.parent != agent || op.done {
			continue
		}
		op.cancelRequested = true
		if !op.launched {
			op.cancelLaunch()
		}
	}
	for _, subAgent := range m.agents {
		if subAgent.parent == agent && subAgent.handle != nil {
			handles = append(handles, subAgent.handle)
		}
	}
	m.mu.Unlock()
	// Outside the lock: each cancellation recurses into this method.
	for _, handle := range handles {
		handle.CancelTurn()
	}
}

// Status is a sub-agent as the TUI shows it.
type Status struct {
	Name  string
	Title string
	// Agent is the sub-agent's session — dormant until opened or messaged
	// when restored (see IsDormant); nil while its launch is queued.
	Agent tool.Agent
	// State is that of its latest operation; restored, its last known one.
	State sgptpb.AgentOperation_State
}

// Children lists the sub-agents agent launched, oldest first.
func (m *Manager) Children(agent tool.Agent) []Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	var children []Status
	for _, subAgent := range m.agents {
		if subAgent.parent == agent {
			children = append(children, Status{Name: subAgent.name, Title: subAgent.title, Agent: subAgent.handle, State: agentStateLocked(subAgent)})
		}
	}
	return children
}

// Parent returns the session that launched agent; nil for a chat the user
// opened.
func (m *Manager) Parent(agent tool.Agent) tool.Agent {
	m.mu.Lock()
	defer m.mu.Unlock()
	if subAgent, ok := m.handleToAgent[agent]; ok {
		return subAgent.parent
	}
	return nil
}

// childTools is the grant a sub-agent receives: the requested subset of the
// launcher's tools, or all of them — always with the sub-agent tools, so it
// can talk back. Never wider than the launcher's, which holds them.
func childTools(requested, launcherTools []string) []string {
	tools := slices.Clone(requested)
	if len(tools) == 0 {
		tools = slices.Clone(launcherTools)
	}
	if !slices.Contains(tools, GroupName) {
		tools = append(tools, GroupName)
	}
	return tools
}

var _ tool.Supervisor = (*Manager)(nil)
