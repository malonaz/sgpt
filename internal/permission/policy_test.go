package permission

import (
	"testing"

	aipb "github.com/malonaz/core/genproto/ai/v1"
	"google.golang.org/protobuf/types/known/structpb"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
)

func call(t *testing.T, name string, arguments map[string]any) *aipb.ToolCall {
	t.Helper()
	structArguments, err := structpb.NewStruct(arguments)
	if err != nil {
		t.Fatal(err)
	}
	return &aipb.ToolCall{Name: name, Arguments: structArguments}
}

func newRule(tool, argument, pattern string, mode sgptpb.PermissionMode) *sgptpb.PermissionRule {
	return &sgptpb.PermissionRule{Tool: tool, Argument: argument, Pattern: pattern, Mode: mode}
}

func mustNew(t *testing.T, rules ...*sgptpb.PermissionRule) *Policy {
	t.Helper()
	policy, err := New(rules)
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func TestZeroValueFallsBackToTool(t *testing.T) {
	var policy Policy
	shell := call(t, "exec_shell", map[string]any{"command": "rm -rf /"})
	if got := policy.Decide(shell, false).Mode; got != ModeReview {
		t.Fatalf("mode = %v, want review", got)
	}
	if got := policy.Decide(shell, true).Mode; got != ModeAllow {
		t.Fatalf("mode = %v, want allow", got)
	}
}

func TestRulesFirstMatchWins(t *testing.T) {
	policy := mustNew(t,
		newRule("exec_shell", "command", `^git (log|diff|show)\b`, ModeAllow),
		newRule("exec_shell", "command", `^git`, ModeDeny),
		newRule("replace", "", "", ModeDeny),
	)
	cases := []struct {
		call *aipb.ToolCall
		want sgptpb.PermissionMode
	}{
		{call(t, "exec_shell", map[string]any{"command": "git log -3"}), ModeAllow},
		{call(t, "exec_shell", map[string]any{"command": "git push"}), ModeDeny},
		{call(t, "exec_shell", map[string]any{"command": "ls"}), ModeReview},
		{call(t, "replace", map[string]any{"path": "a.go"}), ModeDeny},
		{call(t, "read_files", map[string]any{"paths": []any{"a.go"}}), ModeReview},
	}
	for _, c := range cases {
		if got := policy.Decide(c.call, false).Mode; got != c.want {
			t.Errorf("%s %v: mode = %v, want %v", c.call.GetName(), c.call.GetArguments().AsMap(), got, c.want)
		}
	}
}

func TestPatternFansOutOverRepeatedFields(t *testing.T) {
	policy := mustNew(t, newRule("replace", "patches.search", `password`, ModeDeny))
	patches := map[string]any{"patches": []any{
		map[string]any{"search": "x", "replace": "y"},
		map[string]any{"search": "password = 1", "replace": "z"},
	}}
	if got := policy.Decide(call(t, "replace", patches), false).Mode; got != ModeDeny {
		t.Fatalf("mode = %v, want deny", got)
	}
	safe := map[string]any{"patches": []any{map[string]any{"search": "x", "replace": "y"}}}
	if got := policy.Decide(call(t, "replace", safe), false).Mode; got != ModeReview {
		t.Fatalf("mode = %v, want review", got)
	}
}

func TestGrantOverridesRules(t *testing.T) {
	policy := mustNew(t, newRule("exec_shell", "", "", ModeDeny))
	shell := call(t, "exec_shell", map[string]any{"command": "ls"})
	policy.Grant("exec_shell")
	if got := policy.Decide(shell, false).Mode; got != ModeAllow {
		t.Fatalf("mode = %v, want allow after grant", got)
	}
}

func TestChildInheritsRules(t *testing.T) {
	child := mustNew(t, newRule("exec_shell", "command", `^rm`, ModeDeny)).Child()
	if got := child.Decide(call(t, "exec_shell", map[string]any{"command": "rm x"}), false).Mode; got != ModeDeny {
		t.Fatalf("mode = %v, want deny inherited from the parent", got)
	}
}

func TestParentGrantFlowsToChildLive(t *testing.T) {
	parent := mustNew(t)
	child := parent.Child()
	shell := call(t, "exec_shell", map[string]any{"command": "ls"})
	if got := child.Decide(shell, false).Mode; got != ModeReview {
		t.Fatalf("mode = %v, want review before grant", got)
	}
	parent.Grant("exec_shell")
	if got := child.Decide(shell, false).Mode; got != ModeAllow {
		t.Fatalf("mode = %v, want allow after parent grant", got)
	}
}

func TestChildGrantStaysInChild(t *testing.T) {
	parent := mustNew(t)
	child := parent.Child()
	child.Grant("exec_shell")
	shell := call(t, "exec_shell", map[string]any{"command": "ls"})
	if got := child.Decide(shell, false).Mode; got != ModeAllow {
		t.Fatalf("child mode = %v, want allow", got)
	}
	if got := parent.Decide(shell, false).Mode; got != ModeReview {
		t.Fatalf("parent mode = %v, want review: grants never cascade up", got)
	}
}

func TestCompileErrors(t *testing.T) {
	bad := []*sgptpb.PermissionRule{
		newRule("", "", "", ModeAllow),
		newRule("exec_shell", "", "", sgptpb.PermissionMode_PERMISSION_MODE_UNSPECIFIED),
		newRule("exec_shell", "command", "", ModeAllow),
		newRule("exec_shell", "command", "(", ModeAllow),
	}
	for _, r := range bad {
		if _, err := New([]*sgptpb.PermissionRule{r}); err == nil {
			t.Errorf("New(%v): want error", r)
		}
	}
}
