package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	aipb "github.com/malonaz/core/genproto/ai/v1"
	"google.golang.org/protobuf/types/known/structpb"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/permission"
	"github.com/malonaz/sgpt/internal/tool"
)

// fakeLauncher answers every launch after delay, recording the requests and
// the peak number of launches in flight.
type fakeLauncher struct {
	delay    time.Duration
	fail     map[string]bool
	mu       sync.Mutex
	requests []*LaunchRequest
	inFlight atomic.Int32
	peak     atomic.Int32
}

func (l *fakeLauncher) LaunchAgent(ctx context.Context, request *LaunchRequest) (*LaunchResult, error) {
	l.mu.Lock()
	l.requests = append(l.requests, request)
	l.mu.Unlock()
	current := l.inFlight.Add(1)
	defer l.inFlight.Add(-1)
	for {
		previous := l.peak.Load()
		if current <= previous || l.peak.CompareAndSwap(previous, current) {
			break
		}
	}
	select {
	case <-time.After(l.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if l.fail[request.Title] {
		return nil, errors.New("boom")
	}
	return &LaunchResult{Chat: "chats/" + request.Title, Response: "done: " + request.Query}, nil
}

func callerContext(t *testing.T, caller tool.Caller) context.Context {
	t.Helper()
	if caller.Policy == nil {
		caller.Policy = &permission.Policy{}
	}
	return tool.WithCaller(context.Background(), func() tool.Caller { return caller })
}

func agentCall(t *testing.T, arguments map[string]any) *aipb.ToolCall {
	t.Helper()
	structArguments, err := structpb.NewStruct(arguments)
	if err != nil {
		t.Fatal(err)
	}
	return &aipb.ToolCall{Id: "call-1", Name: "agent", Arguments: structArguments}
}

func task(title, query string) map[string]any {
	return map[string]any{"title": title, "query": query}
}

func decode(t *testing.T, toolResult *aipb.ToolResult) *sgptpb.AgentResponse {
	t.Helper()
	agentResponse := &sgptpb.AgentResponse{}
	if err := tool.UnmarshalResult(toolResult, agentResponse); err != nil {
		t.Fatal(err)
	}
	return agentResponse
}

func TestExecuteFansOutAndKeepsOrder(t *testing.T) {
	launcher := &fakeLauncher{delay: 20 * time.Millisecond, fail: map[string]bool{"b": true}}
	agentTool := NewTool(&sgptpb.AgentConfiguration{MaxConcurrent: 2})
	agentTool.SetLauncher(launcher)
	ctx := callerContext(t, tool.Caller{Tools: []string{"read_files", "exec_shell"}})

	toolResult, err := agentTool.Execute(ctx, agentCall(t, map[string]any{
		"context": "shared briefing",
		"tasks":   []any{task("a", "qa"), task("b", "qb"), task("c", "qc")},
		"files":   []any{"shared.go"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	results := decode(t, toolResult).GetResults()
	if len(results) != 3 {
		t.Fatalf("results = %d, want 3", len(results))
	}
	if results[0].GetTitle() != "a" || results[0].GetResponse() != "done: qa" || results[0].GetChat() != "chats/a" {
		t.Errorf("result a = %v", results[0])
	}
	if results[1].GetTitle() != "b" || results[1].GetError() != "boom" || results[1].GetResponse() != "" {
		t.Errorf("result b = %v", results[1])
	}
	if results[2].GetResponse() != "done: qc" {
		t.Errorf("result c = %v", results[2])
	}
	if peak := launcher.peak.Load(); peak != 2 {
		t.Errorf("peak concurrency = %d, want 2", peak)
	}
	for _, request := range launcher.requests {
		if request.Context != "shared briefing" || len(request.Files) != 1 || len(request.Tools) != 2 {
			t.Errorf("request %s did not inherit batch settings: %+v", request.Title, request)
		}
	}
}

func TestTaskOverrides(t *testing.T) {
	launcher := &fakeLauncher{}
	agentTool := NewTool(nil)
	agentTool.SetLauncher(launcher)
	ctx := callerContext(t, tool.Caller{Tools: []string{"read_files", "exec_shell"}})

	_, err := agentTool.Execute(ctx, agentCall(t, map[string]any{
		"model": "opus",
		"tools": []any{"read_files"},
		"files": []any{"shared.go"},
		"tasks": []any{map[string]any{"title": "a", "query": "q", "model": "flash", "files": []any{"own.go"}}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	request := launcher.requests[0]
	if request.Model != "flash" {
		t.Errorf("model = %q, want task override", request.Model)
	}
	if strings.Join(request.Files, ",") != "shared.go,own.go" {
		t.Errorf("files = %v", request.Files)
	}
	if strings.Join(request.Tools, ",") != "read_files" {
		t.Errorf("tools = %v", request.Tools)
	}
}

func TestRejections(t *testing.T) {
	agentTool := NewTool(&sgptpb.AgentConfiguration{MaxDepth: 1})
	agentTool.SetLauncher(&fakeLauncher{})
	caller := tool.Caller{Tools: []string{"read_files"}}
	cases := []struct {
		name      string
		ctx       context.Context
		arguments map[string]any
		want      string
	}{
		{"no tasks", callerContext(t, caller), map[string]any{}, "no tasks"},
		{"no query", callerContext(t, caller), map[string]any{"tasks": []any{task("a", " ")}}, "no query"},
		{"tool ceiling", callerContext(t, caller), map[string]any{"tasks": []any{task("a", "q")}, "tools": []any{"exec_shell"}}, "not available"},
		{"too deep", callerContext(t, tool.Caller{Depth: 1}), map[string]any{"tasks": []any{task("a", "q")}}, "nest"},
		{"outside session", context.Background(), map[string]any{"tasks": []any{task("a", "q")}}, "outside a session"},
		{"loosening rule", callerContext(t, caller), map[string]any{
			"tasks":       []any{task("a", "q")},
			"permissions": []any{map[string]any{"tool": "read_files", "mode": "PERMISSION_MODE_ALLOW"}},
		}, "only tighten"},
		{"bad pattern", callerContext(t, caller), map[string]any{
			"tasks":       []any{task("a", "q")},
			"permissions": []any{map[string]any{"tool": "read_files", "argument": "paths", "pattern": "(", "mode": "PERMISSION_MODE_DENY"}},
		}, "error parsing regexp"},
	}
	for _, c := range cases {
		_, err := agentTool.Review(c.ctx, agentCall(t, c.arguments))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want containing %q", c.name, err, c.want)
		}
	}
}

func TestCancelAbortsQueuedLaunches(t *testing.T) {
	launcher := &fakeLauncher{delay: time.Second}
	agentTool := NewTool(&sgptpb.AgentConfiguration{MaxConcurrent: 1})
	agentTool.SetLauncher(launcher)
	ctx, cancel := context.WithCancel(callerContext(t, tool.Caller{}))
	time.AfterFunc(20*time.Millisecond, cancel)

	toolResult, err := agentTool.Execute(ctx, agentCall(t, map[string]any{
		"tasks": []any{task("a", "q"), task("b", "q")},
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range decode(t, toolResult).GetResults() {
		if !strings.Contains(result.GetError(), "context canceled") {
			t.Errorf("%s: error = %q, want cancellation", result.GetTitle(), result.GetError())
		}
	}
	if len(launcher.requests) != 1 {
		t.Errorf("launched %d, want 1 (the queued one never launched)", len(launcher.requests))
	}
}

func TestSystemPrompt(t *testing.T) {
	prompt := SystemPrompt("You are Go expert.", "  Repo uses please.  ")
	for _, want := range []string{"You are Go expert.", "# Sub-agent", "# Briefing\n\nRepo uses please."} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(SystemPrompt("", ""), "Briefing") {
		t.Error("empty briefing must not add a section")
	}
}
