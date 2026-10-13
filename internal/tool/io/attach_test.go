package io

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	aipb "github.com/malonaz/core/genproto/ai/v1"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/malonaz/sgpt/internal/tool"
)

// Minimal valid file headers; content sniffing only inspects the prefix.
var (
	pngHeader = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	pdfHeader = []byte("%PDF-1.7\n")
)

func writeFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func newToolCall(t *testing.T, name, path string) *aipb.ToolCall {
	t.Helper()
	arguments, err := structpb.NewStruct(map[string]any{"path": path})
	if err != nil {
		t.Fatal(err)
	}
	return &aipb.ToolCall{Id: "call_1", Name: name, Arguments: arguments}
}

// withSink returns a turn-like context and the blocks attached through it.
func withSink() (context.Context, *[]*aipb.Block) {
	var attached []*aipb.Block
	ctx := tool.WithAttach(context.Background(), func(blocks ...*aipb.Block) {
		attached = append(attached, blocks...)
	})
	return ctx, &attached
}

func TestReadImageAttachesImage(t *testing.T) {
	ctx, attached := withSink()
	path := writeFile(t, "shot.png", pngHeader)
	toolResult, err := (&ReadImageTool{}).Execute(ctx, newToolCall(t, "read_image", path))
	if err != nil {
		t.Fatal(err)
	}
	if toolResult.GetContent() != "ok" {
		t.Fatalf("result = %q, want ok", toolResult.GetContent())
	}
	if len(*attached) != 1 || (*attached)[0].GetImage().GetMediaType() != "image/png" {
		t.Fatalf("attached = %v, want one image/png block", *attached)
	}
}

func TestReadDocumentAttachesTitledPDF(t *testing.T) {
	ctx, attached := withSink()
	path := writeFile(t, "spec.pdf", pdfHeader)
	if _, err := (&ReadDocumentTool{}).Execute(ctx, newToolCall(t, "read_document", path)); err != nil {
		t.Fatal(err)
	}
	if len(*attached) != 1 {
		t.Fatalf("attached %d blocks, want 1", len(*attached))
	}
	document := (*attached)[0].GetDocument()
	if document.GetMediaType() != "application/pdf" || document.GetTitle() != "spec.pdf" {
		t.Fatalf("document = %v, want a spec.pdf application/pdf document", document)
	}
}

func TestReadAttachableRejectsWrongMediaType(t *testing.T) {
	ctx, attached := withSink()
	path := writeFile(t, "notes.png", []byte("just text"))
	if _, err := (&ReadImageTool{}).Execute(ctx, newToolCall(t, "read_image", path)); err == nil {
		t.Fatal("expected an error for a non-image file")
	}
	if len(*attached) != 0 {
		t.Fatalf("attached %d blocks on failure, want 0", len(*attached))
	}
}

func TestAttachRequiresTurn(t *testing.T) {
	path := writeFile(t, "shot.png", pngHeader)
	if _, err := (&ReadImageTool{}).Execute(context.Background(), newToolCall(t, "read_image", path)); err == nil {
		t.Fatal("expected an error outside a chat turn")
	}
}
