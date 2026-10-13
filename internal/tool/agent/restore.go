package agent

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"

	aipb "github.com/malonaz/core/genproto/ai/v1"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/store"
	"github.com/malonaz/sgpt/internal/tool"
)

// ChatLister finds the chats sub-agents left behind; the store implements it.
type ChatLister interface {
	ListChildChats(ctx context.Context, parentChatIDs []string) ([]*aipb.Chat, error)
}

// turnOutcomeToState reads a restored sub-agent's last turn as the state of
// its last operation.
var turnOutcomeToState = map[sgptpb.AgentMetadata_TurnOutcome]sgptpb.AgentOperation_State{
	sgptpb.AgentMetadata_TURN_OUTCOME_SUCCEEDED: sgptpb.AgentOperation_STATE_SUCCEEDED,
	sgptpb.AgentMetadata_TURN_OUTCOME_FAILED:    sgptpb.AgentOperation_STATE_FAILED,
	sgptpb.AgentMetadata_TURN_OUTCOME_CANCELLED: sgptpb.AgentOperation_STATE_CANCELLED,
}

// Restore registers the sub-agents root's chat launched in earlier runs, down
// to the nesting cap, as dormant: they show, answer to their recorded names
// and resolve their recorded operations, but load nothing until opened or
// messaged. One list call per level; ctx also scopes their eventual loads.
func (m *Manager) Restore(ctx context.Context, root tool.Agent) error {
	rootID := store.ChatID(root.Chat().GetName())
	if rootID == "" {
		return nil
	}
	idToLauncher := map[string]tool.Agent{rootID: root}
	for depth := 0; depth < m.maxDepth && len(idToLauncher) > 0; depth++ {
		chats, err := m.chats.ListChildChats(ctx, slices.Sorted(maps.Keys(idToLauncher)))
		if err != nil {
			return err
		}
		next := make(map[string]tool.Agent, len(chats))
		m.mu.Lock()
		for _, chat := range chats {
			subAgent := m.restoreLocked(ctx, idToLauncher[store.ParentChatID(chat)], chat)
			next[store.ChatID(chat.GetName())] = subAgent.handle
		}
		// Recorded operations now resolve to these sub-agents.
		m.broadcastLocked()
		m.mu.Unlock()
		idToLauncher = next
	}
	return nil
}

// restoreLocked registers one restored sub-agent below launcher; one still
// known from earlier in this run moves to the reopened launcher instead.
// Caller holds the lock.
func (m *Manager) restoreLocked(ctx context.Context, launcher tool.Agent, chat *aipb.Chat) *subAgent {
	metadata := store.AgentMetadata(chat)
	// Chats launched before metadata was recorded get a name no transcript
	// holds.
	name := cmp.Or(metadata.GetName(), "agents/"+store.ChatID(chat.GetName()))
	if subAgent, ok := m.nameToAgent[name]; ok {
		subAgent.parent = launcher
		return subAgent
	}
	subAgent := &subAgent{
		name:          name,
		title:         chat.GetTitle(),
		parent:        launcher,
		restoredState: turnOutcomeToState[metadata.GetLastTurnOutcome()],
	}
	m.addAgentLocked(subAgent)
	m.bindLocked(subAgent, &dormantAgent{ctx: ctx, m: m, subAgent: subAgent, chat: chat})
	return subAgent
}

// Wake returns agent's session, loading a dormant sub-agent's first.
func (m *Manager) Wake(agent tool.Agent) (tool.Agent, error) {
	if dormant, ok := agent.(*dormantAgent); ok {
		return dormant.wake()
	}
	return agent, nil
}

// IsDormant reports whether agent stands in for a restored sub-agent whose
// session is not loaded.
func IsDormant(agent tool.Agent) bool {
	_, ok := agent.(*dormantAgent)
	return ok
}

// DormantAgent returns the dormant sub-agent whose chat is chatName; nil
// when there is none.
func (m *Manager) DormantAgent(chatName string) tool.Agent {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, subAgent := range m.agents {
		if dormant, ok := subAgent.handle.(*dormantAgent); ok && dormant.chat.GetName() == chatName {
			return dormant
		}
	}
	return nil
}

// grantor is a loaded session, which knows what it hands down to the
// sub-agents it opens.
type grantor interface {
	Caller() tool.Caller
}

// dormantAgent stands in for a restored sub-agent until its session is
// needed: delivering to it loads the session first.
type dormantAgent struct {
	ctx      context.Context
	m        *Manager
	subAgent *subAgent
	chat     *aipb.Chat

	// mu serializes wakes; live is the loaded session, which stale holders
	// of the stand-in still reach.
	mu   sync.Mutex
	live tool.Agent
}

func (d *dormantAgent) Chat() *aipb.Chat     { return d.chat }
func (d *dormantAgent) AwaitingReview() bool { return false }
func (d *dormantAgent) Price() float64       { return d.chat.GetPrice() }

// Inbox never fires: nothing is queued for a session not loaded.
func (d *dormantAgent) Inbox() <-chan struct{} { return nil }

// CancelTurn has no turn of its own to stop, but sub-agents opened below it
// may run.
func (d *dormantAgent) CancelTurn() { d.m.CancelDescendants(d) }

func (d *dormantAgent) Deliver(message *aipb.Message) (<-chan tool.TurnOutcome, error) {
	live, err := d.wake()
	if err != nil {
		return nil, err
	}
	return live.Deliver(message)
}

// wake loads the sub-agent's session, once, under its launcher: the grant
// it gets is the launcher's to hand down, so a dormant launcher wakes first.
func (d *dormantAgent) wake() (tool.Agent, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.live != nil {
		return d.live, nil
	}
	d.m.mu.Lock()
	parent, launcher := d.subAgent.parent, d.m.launcher
	d.m.mu.Unlock()
	if launcher == nil {
		return nil, errNoLauncher
	}
	parent, err := d.m.Wake(parent)
	if err != nil {
		return nil, err
	}
	launchingSession, ok := parent.(grantor)
	if !ok {
		return nil, fmt.Errorf("%s's launcher cannot reopen it", d.subAgent.name)
	}
	handle, err := launcher.LaunchAgent(d.ctx, resumeRequest(launchingSession.Caller(), d.subAgent.name, d.chat))
	if err != nil {
		return nil, fmt.Errorf("reopening %s: %w", d.subAgent.name, err)
	}
	d.m.mu.Lock()
	d.m.bindLocked(d.subAgent, handle)
	d.m.broadcastLocked()
	d.m.mu.Unlock()
	d.live = handle
	return handle, nil
}

// resumeRequest reopens a dormant sub-agent's chat under caller, its
// launcher, with what it was launched with: the grant the user approved,
// capped by what the launcher holds now. Chats launched before metadata was
// recorded get what the launcher hands down.
func resumeRequest(caller tool.Caller, name string, chat *aipb.Chat) *LaunchRequest {
	metadata := store.AgentMetadata(chat)
	tools := slices.DeleteFunc(childTools(metadata.GetTools(), caller.Tools), func(toolName string) bool {
		return toolName != GroupName && !slices.Contains(caller.Tools, toolName)
	})
	return &LaunchRequest{
		Caller:          caller,
		Name:            name,
		Chat:            chat,
		Title:           chat.GetTitle(),
		Tools:           tools,
		Model:           store.CurrentModel(chat),
		ReasoningEffort: cmp.Or(metadata.GetReasoningEffort(), caller.ReasoningEffort),
	}
}

var _ tool.Agent = (*dormantAgent)(nil)
