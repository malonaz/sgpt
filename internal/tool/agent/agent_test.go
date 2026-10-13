package agent

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	aipb "github.com/malonaz/core/genproto/ai/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/tool"
)

// fakeAgent is a session whose turns end when the test says so, or at once
// with reply's answer when set.
type fakeAgent struct {
	reply func(text string) tool.TurnOutcome

	mu        sync.Mutex
	delivered []string
	// unread counts delivered messages no generation has consumed yet.
	unread  int
	pending []chan tool.TurnOutcome
}

func newFakeAgent() *fakeAgent { return &fakeAgent{} }

func (a *fakeAgent) Chat() *aipb.Chat     { return &aipb.Chat{} }
func (a *fakeAgent) AwaitingReview() bool { return false }
func (a *fakeAgent) Price() float64       { return 0 }

func (a *fakeAgent) Deliver(message *aipb.Message) (<-chan tool.TurnOutcome, error) {
	text := message.GetBlocks()[0].GetText()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.delivered = append(a.delivered, text)
	a.unread++
	outcomeCh := make(chan tool.TurnOutcome, 1)
	if a.reply != nil {
		outcomeCh <- a.reply(text)
	} else {
		a.pending = append(a.pending, outcomeCh)
	}
	return outcomeCh, nil
}

func (a *fakeAgent) CancelTurn() { a.finish(tool.TurnOutcome{Err: context.Canceled}) }

// Inbox is closed while a message is unread, like a session's; tests block
// on it only once something is.
func (a *fakeAgent) Inbox() <-chan struct{} {
	a.mu.Lock()
	defer a.mu.Unlock()
	inbox := make(chan struct{})
	if a.unread > 0 {
		close(inbox)
	}
	return inbox
}

// consume marks the delivered messages as read by a generation.
func (a *fakeAgent) consume() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.unread = 0
}

// finish ends every turn in flight with outcome.
func (a *fakeAgent) finish(outcome tool.TurnOutcome) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, outcomeCh := range a.pending {
		outcomeCh <- outcome
	}
	a.pending = nil
}

func (a *fakeAgent) messages() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.delivered)
}

// fakeLauncher opens fake sub-agents and hands them to the test.
type fakeLauncher struct {
	reply    func(text string) tool.TurnOutcome
	fail     map[string]bool
	launched chan *fakeAgent

	mu       sync.Mutex
	requests []*LaunchRequest
}

func newFakeLauncher() *fakeLauncher { return &fakeLauncher{launched: make(chan *fakeAgent, 16)} }

func (l *fakeLauncher) LaunchAgent(_ context.Context, request *LaunchRequest) (tool.Agent, error) {
	l.mu.Lock()
	l.requests = append(l.requests, request)
	l.mu.Unlock()
	if l.fail[request.Title] {
		return nil, errors.New("boom")
	}
	subAgent := newFakeAgent()
	subAgent.reply = l.reply
	l.launched <- subAgent
	return subAgent, nil
}

func (l *fakeLauncher) awaitLaunch(t *testing.T) *fakeAgent {
	t.Helper()
	select {
	case subAgent := <-l.launched:
		return subAgent
	case <-time.After(2 * time.Second):
		t.Fatal("no sub-agent launched")
		return nil
	}
}

func callerContext(caller tool.Caller) context.Context {
	return tool.WithCaller(context.Background(), func() tool.Caller { return caller })
}

func call(t *testing.T, name string, arguments map[string]any) *aipb.ToolCall {
	t.Helper()
	structArguments, err := structpb.NewStruct(arguments)
	if err != nil {
		t.Fatal(err)
	}
	return &aipb.ToolCall{Id: "call-" + name, Name: name, Arguments: structArguments}
}

func task(title, query string) map[string]any {
	return map[string]any{"title": title, "query": query}
}

// execute runs a sub-agent tool and decodes its response.
func execute[T proto.Message](t *testing.T, m *Manager, ctx context.Context, handlerID string, arguments map[string]any, response T) T {
	t.Helper()
	toolResult, err := m.Tools()[handlerID].Execute(ctx, call(t, handlerID, arguments))
	if err != nil {
		t.Fatal(err)
	}
	if err := tool.UnmarshalResult(toolResult, response); err != nil {
		t.Fatal(err)
	}
	return response
}

// awaitWatched returns once a wait has registered on the operation.
func awaitWatched(t *testing.T, m *Manager, name string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		m.mu.Lock()
		watched := m.nameToOperation[name].watchers > 0
		m.mu.Unlock()
		if watched {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no wait registered on %s", name)
		}
		time.Sleep(time.Millisecond)
	}
}

func awaitMessages(t *testing.T, agent *fakeAgent, count int) []string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for len(agent.messages()) < count {
		if time.Now().After(deadline) {
			t.Fatalf("messages = %q, want %d", agent.messages(), count)
		}
		time.Sleep(time.Millisecond)
	}
	return agent.messages()
}

// A launch returns at once; at most max_concurrent first turns run per level,
// and results a wait collects are not also reported as messages.
func TestCreateAgentsIsAsyncBoundedAndWaitable(t *testing.T) {
	m := NewManager(&sgptpb.AgentConfiguration{MaxConcurrent: 2})
	launcher := newFakeLauncher()
	m.SetLauncher(launcher)
	parent := newFakeAgent()
	ctx := callerContext(tool.Caller{Agent: parent, Tools: []string{"read_files", GroupName}})

	created := execute(t, m, ctx, tool.HandlerIDCreateAgents, map[string]any{
		"context": "shared briefing",
		"tools":   []any{"read_files"},
		"tasks":   []any{task("a", "qa"), task("b", "qb"), map[string]any{"title": "c", "query": "qc", "model": "flash"}},
	}, &sgptpb.CreateAgentsResponse{})
	if len(created.GetOperations()) != 3 {
		t.Fatalf("operations = %v, want 3", created.GetOperations())
	}

	first, second := launcher.awaitLaunch(t), launcher.awaitLaunch(t)
	select {
	case <-launcher.launched:
		t.Fatal("a third sub-agent ran beyond max_concurrent")
	case <-time.After(50 * time.Millisecond):
	}

	var names []any
	for _, op := range created.GetOperations() {
		names = append(names, op.GetName())
	}
	waited := make(chan *sgptpb.WaitOperationsResponse, 1)
	go func() {
		waited <- execute(t, m, ctx, tool.HandlerIDWaitOperations, map[string]any{"names": names, "return_when": "RETURN_WHEN_ALL"}, &sgptpb.WaitOperationsResponse{})
	}()
	awaitWatched(t, m, created.GetOperations()[0].GetName())
	// Tasks launch concurrently, in any order: each answers by its query.
	settle := func(subAgent *fakeAgent) {
		query := awaitMessages(t, subAgent, 1)[0]
		if query == "qc" {
			subAgent.finish(tool.TurnOutcome{Err: errors.New("boom")})
			return
		}
		subAgent.finish(tool.TurnOutcome{Text: "report on " + query})
	}
	settle(first)
	third := launcher.awaitLaunch(t)
	settle(second)
	settle(third)

	response := <-waited
	if response.GetReason() != sgptpb.WaitOperationsResponse_REASON_COMPLETED {
		t.Fatalf("reason = %v", response.GetReason())
	}
	got := response.GetOperations()
	if got[0].GetResponse() != "report on qa" || got[1].GetResponse() != "report on qb" || got[2].GetState() != sgptpb.AgentOperation_STATE_FAILED {
		t.Fatalf("operations = %v", got)
	}
	if messages := parent.messages(); len(messages) != 0 {
		t.Fatalf("parent got %q, want the results through the wait only", messages)
	}
	for _, request := range launcher.requests {
		if request.Context != "shared briefing" || !slices.Equal(request.Tools, []string{"read_files", GroupName}) {
			t.Errorf("request %s = %+v, want the batch settings and the sub-agent tools", request.Title, request)
		}
	}
	for _, request := range launcher.requests {
		if (request.Title == "c") != (request.Model == "flash") {
			t.Errorf("request %s model = %q, want only task c's override", request.Title, request.Model)
		}
	}
}

// A result nobody waits for is reported to the launcher as a message: that is
// what wakes an idle launcher.
func TestUnwatchedResultIsReportedToTheLauncher(t *testing.T) {
	m := NewManager(nil)
	launcher := newFakeLauncher()
	launcher.reply = func(string) tool.TurnOutcome { return tool.TurnOutcome{Text: "the report"} }
	launcher.fail = map[string]bool{"broken": true}
	m.SetLauncher(launcher)
	parent := newFakeAgent()
	ctx := callerContext(tool.Caller{Agent: parent})

	execute(t, m, ctx, tool.HandlerIDCreateAgents, map[string]any{"tasks": []any{task("explore", "q"), task("broken", "q")}}, &sgptpb.CreateAgentsResponse{})

	messages := strings.Join(awaitMessages(t, parent, 2), "\n")
	if !strings.Contains(messages, "the report") || !strings.Contains(messages, "boom") {
		t.Fatalf("parent messages = %q, want the report and the failure", messages)
	}
}

// A sub-agent's question must reach a launcher blocked on that very
// sub-agent, or both wait forever.
func TestWaitReturnsWhenAMessageArrives(t *testing.T) {
	m := NewManager(nil)
	launcher := newFakeLauncher()
	m.SetLauncher(launcher)
	parent := newFakeAgent()
	ctx := callerContext(tool.Caller{Agent: parent})
	execute(t, m, ctx, tool.HandlerIDCreateAgents, map[string]any{"tasks": []any{task("a", "q")}}, &sgptpb.CreateAgentsResponse{})
	subAgent := launcher.awaitLaunch(t)
	// Its task arrives once the manager knows it, so it can reach "parent".
	awaitMessages(t, subAgent, 1)

	waited := make(chan *sgptpb.WaitOperationsResponse, 1)
	go func() {
		waited <- execute(t, m, ctx, tool.HandlerIDWaitOperations, map[string]any{}, &sgptpb.WaitOperationsResponse{})
	}()
	subCtx := callerContext(tool.Caller{Agent: subAgent, Depth: 1})
	execute(t, m, subCtx, tool.HandlerIDMessageAgent, map[string]any{"agent": "parent", "text": "which file?"}, &sgptpb.MessageAgentResponse{})

	select {
	case response := <-waited:
		if response.GetReason() != sgptpb.WaitOperationsResponse_REASON_MESSAGE {
			t.Fatalf("reason = %v, want the message", response.GetReason())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the wait missed the sub-agent's question")
	}
	if messages := parent.messages(); len(messages) != 1 || !strings.Contains(messages[0], "which file?") {
		t.Fatalf("parent messages = %q", messages)
	}
}

// Following up with a sub-agent is another operation, resolving to its reply;
// only the launcher may address it.
func TestMessageAgentFollowUp(t *testing.T) {
	m := NewManager(nil)
	launcher := newFakeLauncher()
	launcher.reply = func(text string) tool.TurnOutcome { return tool.TurnOutcome{Text: "re: " + text} }
	m.SetLauncher(launcher)
	parent := newFakeAgent()
	ctx := callerContext(tool.Caller{Agent: parent})
	created := execute(t, m, ctx, tool.HandlerIDCreateAgents, map[string]any{"tasks": []any{task("a", "q")}}, &sgptpb.CreateAgentsResponse{})
	launcher.awaitLaunch(t)
	agentName := created.GetOperations()[0].GetAgent()
	awaitMessages(t, parent, 1)
	parent.consume()

	followUp := execute(t, m, ctx, tool.HandlerIDMessageAgent, map[string]any{"agent": agentName, "text": "and tests?"}, &sgptpb.MessageAgentResponse{})
	waited := execute(t, m, ctx, tool.HandlerIDWaitOperations, map[string]any{"names": []any{followUp.GetOperation().GetName()}}, &sgptpb.WaitOperationsResponse{})
	if response := waited.GetOperations()[0].GetResponse(); !strings.Contains(response, "and tests?") {
		t.Fatalf("follow-up response = %q", response)
	}

	strangerCtx := callerContext(tool.Caller{Agent: newFakeAgent()})
	if _, err := m.Tools()[tool.HandlerIDMessageAgent].Execute(strangerCtx, call(t, "message_agent", map[string]any{"agent": agentName, "text": "hi"})); err == nil {
		t.Fatal("a chat messaged a sub-agent it did not launch")
	}
}

// Stopping a chat stops its sub-agents silently: reporting the cancellations
// would wake the chat the user just stopped.
func TestCancelDescendantsIsNotReportedBack(t *testing.T) {
	m := NewManager(&sgptpb.AgentConfiguration{MaxConcurrent: 1})
	launcher := newFakeLauncher()
	m.SetLauncher(launcher)
	parent := newFakeAgent()
	ctx := callerContext(tool.Caller{Agent: parent})
	created := execute(t, m, ctx, tool.HandlerIDCreateAgents, map[string]any{"tasks": []any{task("running", "q"), task("queued", "q")}}, &sgptpb.CreateAgentsResponse{})
	launcher.awaitLaunch(t)

	m.CancelDescendants(parent)

	names := []any{created.GetOperations()[0].GetName(), created.GetOperations()[1].GetName()}
	got := execute(t, m, ctx, tool.HandlerIDWaitOperations, map[string]any{"names": names, "return_when": "RETURN_WHEN_ALL"}, &sgptpb.WaitOperationsResponse{})
	for _, op := range got.GetOperations() {
		if op.GetState() != sgptpb.AgentOperation_STATE_CANCELLED {
			t.Fatalf("operation %v, want cancelled", op)
		}
	}
	time.Sleep(20 * time.Millisecond)
	if messages := parent.messages(); len(messages) != 0 {
		t.Fatalf("parent got %q after stopping its sub-agents", messages)
	}
}

func TestCreateAgentsRejections(t *testing.T) {
	m := NewManager(&sgptpb.AgentConfiguration{MaxDepth: 1})
	m.SetLauncher(newFakeLauncher())
	caller := tool.Caller{Agent: newFakeAgent(), Tools: []string{"read_files"}}
	deep := caller
	deep.Depth = 1
	cases := []struct {
		name      string
		ctx       context.Context
		arguments map[string]any
		want      string
	}{
		{"no tasks", callerContext(caller), map[string]any{}, "no tasks"},
		{"no query", callerContext(caller), map[string]any{"tasks": []any{task("a", " ")}}, "no query"},
		{"tool ceiling", callerContext(caller), map[string]any{"tasks": []any{task("a", "q")}, "tools": []any{"exec_shell"}}, "not available"},
		{"too deep", callerContext(deep), map[string]any{"tasks": []any{task("a", "q")}}, "nest"},
		{"outside session", context.Background(), map[string]any{"tasks": []any{task("a", "q")}}, "outside a session"},
	}
	createAgents := m.Tools()[tool.HandlerIDCreateAgents]
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := createAgents.Review(c.ctx, call(t, "create_agents", c.arguments))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
		})
	}
}
