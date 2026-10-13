package agent

import (
	"context"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	aipb "github.com/malonaz/core/genproto/ai/v1"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/store"
	"github.com/malonaz/sgpt/internal/tool"
)

// fakeChats serves the sub-agent chats an earlier run left, recording the
// parent chat IDs of every list call.
type fakeChats struct {
	chats []*aipb.Chat

	mu    sync.Mutex
	calls [][]string
}

func (c *fakeChats) ListChildChats(_ context.Context, parentChatIDs []string) ([]*aipb.Chat, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, parentChatIDs)
	var children []*aipb.Chat
	for _, chat := range c.chats {
		if slices.Contains(parentChatIDs, store.ParentChatID(chat)) {
			children = append(children, chat)
		}
	}
	return children, nil
}

func chatName(id string) string { return "organizations/o/users/u/chats/" + id }

// agentChat is a sub-agent chat as an earlier run persisted it; nil metadata
// for one launched before it was recorded.
func agentChat(t *testing.T, id, parentID string, metadata *sgptpb.AgentMetadata) *aipb.Chat {
	t.Helper()
	chat := &aipb.Chat{Name: chatName(id), Title: "task " + id}
	store.SetParentChatID(chat, chatName(parentID))
	if metadata != nil {
		if err := store.SetAgentMetadata(chat, metadata); err != nil {
			t.Fatal(err)
		}
	}
	return chat
}

// restoredTree restores root → a → a1 and root → b (launched before agent
// metadata was recorded) below a reopened root chat.
func restoredTree(t *testing.T) (*Manager, *fakeChats, *fakeLauncher, *fakeAgent) {
	t.Helper()
	chats := &fakeChats{chats: []*aipb.Chat{
		agentChat(t, "a", "root", &sgptpb.AgentMetadata{
			Name:            "agents/0000000a",
			Tools:           []string{"read_files", GroupName},
			ReasoningEffort: aipb.ReasoningEffort_REASONING_EFFORT_HIGH,
			LastTurnOutcome: sgptpb.AgentMetadata_TURN_OUTCOME_SUCCEEDED,
		}),
		agentChat(t, "b", "root", nil),
		agentChat(t, "a1", "a", &sgptpb.AgentMetadata{
			Name:            "agents/000000a1",
			Tools:           []string{GroupName},
			LastTurnOutcome: sgptpb.AgentMetadata_TURN_OUTCOME_CANCELLED,
		}),
	}}
	m := NewManager(nil, chats)
	launcher := newFakeLauncher()
	m.SetLauncher(launcher)
	root := newFakeAgent()
	root.chat = &aipb.Chat{Name: chatName("root")}
	root.tools = []string{"read_files", "exec_shell", GroupName}
	if err := m.Restore(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	return m, chats, launcher, root
}

// A reopened chat gets its sub-agent tree back, one list call per level and
// nothing loaded: dormant agents with their last known states.
func TestRestoreRebuildsTheTreeDormant(t *testing.T) {
	m, chats, launcher, root := restoredTree(t)

	if want := [][]string{{"root"}, {"a", "b"}}; !slices.EqualFunc(chats.calls, want, slices.Equal) {
		t.Fatalf("list calls = %q, want %q", chats.calls, want)
	}
	children := m.Children(root)
	if len(children) != 2 {
		t.Fatalf("children = %+v, want a and b", children)
	}
	a, b := children[0], children[1]
	if a.Name != "agents/0000000a" || a.Title != "task a" || a.State != sgptpb.AgentOperation_STATE_SUCCEEDED || !IsDormant(a.Agent) {
		t.Errorf("a = %+v, want dormant, named as recorded, succeeded", a)
	}
	if b.Name != "agents/b" || b.State != sgptpb.AgentOperation_STATE_UNSPECIFIED || !IsDormant(b.Agent) {
		t.Errorf("b = %+v, want dormant, named after its chat, state unknown", b)
	}
	grandchildren := m.Children(a.Agent)
	if len(grandchildren) != 1 || grandchildren[0].State != sgptpb.AgentOperation_STATE_CANCELLED || m.Parent(grandchildren[0].Agent) != a.Agent {
		t.Errorf("a's children = %+v, want a1, cancelled", grandchildren)
	}
	if m.Parent(a.Agent) != root || m.DormantAgent(chatName("a")) != a.Agent {
		t.Error("a is not found from its launcher or its chat")
	}
	if len(launcher.requests) != 0 {
		t.Fatalf("restoring loaded %d sessions, want none", len(launcher.requests))
	}
}

// Recorded snapshots resolve to restored sub-agents: final states stand,
// stale ones take the sub-agent's last known state, or unknown.
func TestRecordedOperationsResolveToRestoredAgents(t *testing.T) {
	m, _, _, _ := restoredTree(t)

	got := m.liveSnapshots([]*sgptpb.AgentOperation{
		{Name: "operations/1", Agent: "agents/0000000a", State: sgptpb.AgentOperation_STATE_RUNNING},
		{Name: "operations/2", Agent: "agents/0000000a", State: sgptpb.AgentOperation_STATE_FAILED, Error: "boom"},
		{Name: "operations/3", Agent: "agents/gone", State: sgptpb.AgentOperation_STATE_QUEUED},
	})
	want := []sgptpb.AgentOperation_State{
		sgptpb.AgentOperation_STATE_SUCCEEDED,
		sgptpb.AgentOperation_STATE_FAILED,
		sgptpb.AgentOperation_STATE_UNSPECIFIED,
	}
	for i, snapshot := range got {
		if snapshot.GetState() != want[i] {
			t.Errorf("snapshot %s state = %v, want %v", snapshot.GetName(), snapshot.GetState(), want[i])
		}
	}
	if got[1].GetError() != "boom" {
		t.Errorf("final snapshot = %v, want it as recorded", got[1])
	}
}

// Messaging a dormant sub-agent loads its session under its launcher, with
// what it was launched with, then runs the turn like for an idle one.
func TestMessagingADormantAgentLoadsIt(t *testing.T) {
	m, _, launcher, root := restoredTree(t)
	launcher.reply = func(text string) tool.TurnOutcome { return tool.TurnOutcome{Text: "re: " + text} }
	dormant := m.Children(root)[0].Agent
	ctx := callerContext(root.Caller())

	sent := execute(t, m, ctx, tool.HandlerIDMessageAgent, map[string]any{"agent": "agents/0000000a", "text": "and tests?"}, &sgptpb.MessageAgentResponse{})
	live := launcher.awaitLaunch(t)
	waited := execute(t, m, ctx, tool.HandlerIDWaitOperations, map[string]any{"names": []any{sent.GetOperation().GetName()}}, &sgptpb.WaitOperationsResponse{})
	if response := waited.GetOperations()[0].GetResponse(); !strings.Contains(response, "and tests?") {
		t.Fatalf("response = %q, want the reopened sub-agent's reply", response)
	}

	request := launcher.requests[0]
	if request.Chat != dormant.Chat() || request.Name != "agents/0000000a" || request.Caller.Agent != root {
		t.Errorf("request = %+v, want a's chat reopened under root", request)
	}
	if !slices.Equal(request.Tools, []string{"read_files", GroupName}) || request.ReasoningEffort != aipb.ReasoningEffort_REASONING_EFFORT_HIGH {
		t.Errorf("tools = %q, reasoning effort = %v, want those recorded", request.Tools, request.ReasoningEffort)
	}
	if a := m.Children(root)[0]; a.Agent != live || IsDormant(a.Agent) || len(m.Children(live)) != 1 {
		t.Errorf("a = %+v, want its loaded session, launching a1", a)
	}
	// Stale holders of the stand-in reach the session; nothing reloads.
	if _, err := dormant.Deliver(store.NewAgentMessage(launcherSender, "again")); err != nil {
		t.Fatal(err)
	}
	if awaitMessages(t, live, 2); len(launcher.requests) != 1 {
		t.Fatalf("%d loads, want one", len(launcher.requests))
	}
}

// Opening a nested dormant sub-agent wakes its launcher first: the grant is
// the launcher's to hand down. Concurrent wakes load once.
func TestWakingANestedAgentWakesItsLauncherFirst(t *testing.T) {
	m, _, launcher, root := restoredTree(t)
	a1 := m.Children(m.Children(root)[0].Agent)[0].Agent

	var wg sync.WaitGroup
	lives := make([]tool.Agent, 2)
	for i := range lives {
		wg.Go(func() {
			live, err := m.Wake(a1)
			if err != nil {
				t.Error(err)
			}
			lives[i] = live
		})
	}
	wg.Wait()

	if len(launcher.requests) != 2 || lives[0] != lives[1] {
		t.Fatalf("%d loads, sessions %v, want a then a1, once", len(launcher.requests), lives)
	}
	liveA := m.Children(root)[0].Agent
	if launcher.requests[0].Caller.Agent != root || launcher.requests[1].Caller.Agent != liveA || launcher.requests[1].Caller.Depth != 1 {
		t.Errorf("requests = %+v, want a under root, then a1 under a", launcher.requests)
	}
	if m.Parent(lives[0]) != liveA {
		t.Error("a1 lost its launcher")
	}
}

// A chat reopened in the run that launched its sub-agents adopts them
// rather than registering them twice; their names are what the launch
// recorded on their chats.
func TestRestoreAdoptsSubAgentsOfThisRun(t *testing.T) {
	chats := &fakeChats{}
	m := NewManager(nil, chats)
	launcher := newFakeLauncher()
	m.SetLauncher(launcher)
	closed := newFakeAgent()
	closed.chat = &aipb.Chat{Name: chatName("root")}
	created := execute(t, m, callerContext(closed.Caller()), tool.HandlerIDCreateAgents, map[string]any{"tasks": []any{task("a", "q")}}, &sgptpb.CreateAgentsResponse{})
	live := launcher.awaitLaunch(t)
	snapshot := created.GetOperations()[0]
	if !regexp.MustCompile(`^agents/[0-9a-f]{8}$`).MatchString(snapshot.GetAgent()) || !regexp.MustCompile(`^operations/[0-9a-f]{8}$`).MatchString(snapshot.GetName()) {
		t.Fatalf("names = %s, %s, want random IDs", snapshot.GetAgent(), snapshot.GetName())
	}
	if launcher.requests[0].Name != snapshot.GetAgent() {
		t.Fatalf("launch named %q, want %q recorded on the chat", launcher.requests[0].Name, snapshot.GetAgent())
	}
	chats.chats = []*aipb.Chat{agentChat(t, "a", "root", &sgptpb.AgentMetadata{Name: snapshot.GetAgent()})}

	reopened := newFakeAgent()
	reopened.chat = closed.chat
	if err := m.Restore(context.Background(), reopened); err != nil {
		t.Fatal(err)
	}
	children := m.Children(reopened)
	if len(children) != 1 || children[0].Agent != live || len(m.Children(closed)) != 0 {
		t.Fatalf("children = %+v, want the running sub-agent, moved over", children)
	}
}

// A reopened sub-agent gets what it was launched with, its grant capped by
// what its launcher holds now; one launched before metadata was recorded
// gets what the launcher hands down.
func TestResumeRequest(t *testing.T) {
	low, high := aipb.ReasoningEffort_REASONING_EFFORT_LOW, aipb.ReasoningEffort_REASONING_EFFORT_HIGH
	caller := tool.Caller{Tools: []string{"read_files", GroupName}, ReasoningEffort: low}
	cases := []struct {
		name       string
		metadata   *sgptpb.AgentMetadata
		wantTools  []string
		wantEffort aipb.ReasoningEffort
	}{
		{"recorded", &sgptpb.AgentMetadata{Tools: []string{"read_files", GroupName}, ReasoningEffort: high}, []string{"read_files", GroupName}, high},
		{"capped by the launcher", &sgptpb.AgentMetadata{Tools: []string{"exec_shell", GroupName}}, []string{GroupName}, low},
		{"unrecorded", nil, []string{"read_files", GroupName}, low},
	}
	for _, c := range cases {
		request := resumeRequest(caller, "agents/x", agentChat(t, "x", "root", c.metadata))
		if !slices.Equal(request.Tools, c.wantTools) || request.ReasoningEffort != c.wantEffort {
			t.Errorf("%s: tools = %q, reasoning effort = %v, want %q, %v", c.name, request.Tools, request.ReasoningEffort, c.wantTools, c.wantEffort)
		}
	}
}
