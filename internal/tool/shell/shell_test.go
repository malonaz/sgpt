package shell

import (
	"context"
	"testing"

	aipb "github.com/malonaz/core/genproto/ai/v1"
	"google.golang.org/protobuf/types/known/structpb"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/tool"
)

func execute(t *testing.T, command string) *sgptpb.ExecShellResponse {
	t.Helper()
	arguments, err := structpb.NewStruct(map[string]any{"command": command})
	if err != nil {
		t.Fatal(err)
	}
	toolResult, err := (&Tool{}).Execute(context.Background(), &aipb.ToolCall{Id: "call-1", Name: "exec_shell", Arguments: arguments})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	response := &sgptpb.ExecShellResponse{}
	if err := tool.UnmarshalResult(toolResult, response); err != nil {
		t.Fatal(err)
	}
	return response
}

// Binary output must still reach the model rather than fail the result.
func TestExecuteNonUTF8Output(t *testing.T) {
	if got := execute(t, `printf 'ok\377'`); got.GetOutput() != "ok\uFFFD" || got.GetError() != "" {
		t.Fatalf("response = %v, want the output with the invalid byte replaced", got)
	}
}
