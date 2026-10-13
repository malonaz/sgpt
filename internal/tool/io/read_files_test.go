package io

import (
	"context"
	"testing"

	aipb "github.com/malonaz/core/genproto/ai/v1"
	"google.golang.org/protobuf/types/known/structpb"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/tool"
)

// A binary file fails on its own; the rest of the batch is still read.
func TestReadFilesBinaryFileFailsAlone(t *testing.T) {
	textPath := writeFile(t, "a.txt", []byte("hello"))
	binaryPath := writeFile(t, "b.bin", pngHeader)
	arguments, err := structpb.NewStruct(map[string]any{"paths": []any{textPath, binaryPath}})
	if err != nil {
		t.Fatal(err)
	}
	toolResult, err := (&ReadFilesTool{}).Execute(context.Background(), &aipb.ToolCall{Id: "call_1", Name: "read_files", Arguments: arguments})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	response := &sgptpb.ReadFilesResponse{}
	if err := tool.UnmarshalResult(toolResult, response); err != nil {
		t.Fatal(err)
	}
	files := response.GetFiles()
	if len(files) != 2 || files[0].GetContent() != "hello" || files[1].GetError() == "" {
		t.Fatalf("files = %v, want the text read and the binary file failed", files)
	}
}
