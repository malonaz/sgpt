package io

import (
	"context"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	aipb "github.com/malonaz/core/genproto/ai/v1"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/tool"
)

// ReadFiles is the tool definition for file reading, built from the
// ToolService.ReadFiles method.
var ReadFiles = tool.MustBuildTool("read_files", tool.HandlerIDReadFiles, "sgpt.v1.ToolService.ReadFiles")

func parseReadFilesArguments(toolCall *aipb.ToolCall) (*sgptpb.ReadFilesRequest, error) {
	readFilesRequest := &sgptpb.ReadFilesRequest{}
	if err := tool.UnmarshalArguments(toolCall, readFilesRequest); err != nil {
		return nil, err
	}
	if len(readFilesRequest.GetPaths()) == 0 {
		return nil, fmt.Errorf("no paths specified")
	}
	return readFilesRequest, nil
}

// ReadFilesTool reads files from the user's system.
type ReadFilesTool struct{}

func (t *ReadFilesTool) Review(_ context.Context, toolCall *aipb.ToolCall) (*sgptpb.ToolCallMetadata, error) {
	// Validate arguments even though the metadata no longer uses them.
	if _, err := parseReadFilesArguments(toolCall); err != nil {
		return nil, err
	}
	// Auto-execution is declared on the proto method (NO_SIDE_EFFECTS).
	// No display message: the row already names the files.
	return &sgptpb.ToolCallMetadata{
		DisplayMessage: &sgptpb.DisplayMessage{},
		AutoExecute:    tool.NoSideEffects(toolCall),
	}, nil
}

func (t *ReadFilesTool) Execute(_ context.Context, toolCall *aipb.ToolCall) (*aipb.ToolResult, error) {
	readFilesRequest, err := parseReadFilesArguments(toolCall)
	if err != nil {
		return nil, err
	}
	files := make([]*sgptpb.ReadFilesResponse_File, 0, len(readFilesRequest.GetPaths()))
	for _, path := range readFilesRequest.GetPaths() {
		file := &sgptpb.ReadFilesResponse_File{Path: path}
		// Per-file errors are part of the result so one bad path doesn't
		// sink the whole read.
		switch content, err := os.ReadFile(path); {
		case err != nil:
			file.Error = err.Error()
		case !utf8.Valid(content):
			// Proto strings must be UTF-8: one binary file would fail the batch.
			file.Error = "not a UTF-8 text file"
		default:
			file.Content = string(content)
		}
		files = append(files, file)
	}
	readFilesResponse := &sgptpb.ReadFilesResponse{Files: files}
	return tool.NewStructuredToolResult(toolCall, readFilesResponse)
}

// Summarize names the files and how much of them was read.
func (t *ReadFilesTool) Summarize(toolCall *aipb.ToolCall, toolResult *aipb.ToolResult) (tool.Summary, bool) {
	summary := tool.Summary{Verb: "Read", Subject: tool.PathsSubject(tool.StringList(tool.Argument(toolCall, "paths")))}
	if toolResult == nil || toolResult.GetError() != nil {
		return summary, true
	}
	files := tool.ResultField(toolResult, "files").GetListValue().GetValues()
	lines, failed, fileError := 0, 0, ""
	for _, file := range files {
		fields := file.GetStructValue().GetFields()
		if fileError = fields["error"].GetStringValue(); fileError != "" {
			failed++
			continue
		}
		lines += lineCount(fields["content"].GetStringValue())
	}
	outcome := tool.Count(lines, "line", "lines")
	if len(files) > 1 {
		outcome = tool.Count(len(files), "file", "files") + " · " + outcome
	}
	switch {
	case len(files) == 1 && failed == 1:
		outcome = errorCause(fileError)
	case failed > 0:
		outcome += fmt.Sprintf(" · %d failed", failed)
	}
	summary.Outcome, summary.Failed = outcome, failed > 0
	return summary, true
}

// RenderResult lists the files read rather than their contents: the
// contents are for the model, the user wants to know what it saw.
func (t *ReadFilesTool) RenderResult(_ *aipb.ToolCall, toolResult *aipb.ToolResult) (string, bool) {
	files := tool.ResultField(toolResult, "files").GetListValue().GetValues()
	if len(files) == 0 {
		return "", false
	}
	var b strings.Builder
	for _, file := range files {
		fields := file.GetStructValue().GetFields()
		path := tool.DisplayPath(fields["path"].GetStringValue())
		if fileError := fields["error"].GetStringValue(); fileError != "" {
			fmt.Fprintf(&b, "- `%s` %s\n", path, fileError)
			continue
		}
		fmt.Fprintf(&b, "- `%s` %s\n", path, tool.Count(lineCount(fields["content"].GetStringValue()), "line", "lines"))
	}
	return b.String(), true
}

// lineCount counts lines the way an editor numbers them.
func lineCount(content string) int {
	if content == "" {
		return 0
	}
	return strings.Count(strings.TrimSuffix(content, "\n"), "\n") + 1
}

// errorCause reduces a file error to its cause ("open x: no such file" reads
// "no such file"): the path is already the row's subject.
func errorCause(fileError string) string {
	if index := strings.LastIndex(fileError, ": "); index != -1 {
		return fileError[index+2:]
	}
	return fileError
}

var (
	_ tool.Tool           = (*ReadFilesTool)(nil)
	_ tool.Summarizer     = (*ReadFilesTool)(nil)
	_ tool.ResultRenderer = (*ReadFilesTool)(nil)
)

func init() { tool.RegisterBuiltin(ReadFiles) }
