package tools

import (
	"context"
	"encoding/json"
	"fmt"

	aipb "github.com/malonaz/core/genproto/ai/v1"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
)

// ToolHandlerIDAnnotation routes a tool call to its registered Tool.
const ToolHandlerIDAnnotation = "sgpt.com/tool-handler-id"

const (
	HandlerIDShell     = "shell"
	HandlerIDReadFiles = "read_files"
	HandlerIDEngine    = "engine"
	HandlerIDEditFile  = "edit_file"
)

// Tool reviews and executes tool calls.
type Tool interface {
	// Review inspects a tool call as it arrives and returns display and
	// auto-execute metadata. It must not have side effects.
	Review(ctx context.Context, toolCall *aipb.ToolCall) (*sgptpb.ToolCallMetadata, error)
	// Execute runs the tool call and returns its result.
	Execute(ctx context.Context, toolCall *aipb.ToolCall) (*aipb.ToolResult, error)
}

// Registry dispatches tool calls to registered tools and owns the tool
// definitions advertised to the model.
type Registry struct {
	handlerIDToTool map[string]Tool
	tools           []*aipb.Tool
	toolSets        []*aipb.ToolSet
}

// NewRegistry instantiates an empty registry.
func NewRegistry() *Registry {
	return &Registry{handlerIDToTool: map[string]Tool{}}
}

// Register binds a handler ID to a tool implementation.
func (r *Registry) Register(handlerID string, tool Tool) {
	r.handlerIDToTool[handlerID] = tool
}

// AddTools advertises tool definitions to the model.
func (r *Registry) AddTools(tools ...*aipb.Tool) {
	r.tools = append(r.tools, tools...)
}

// AddToolSets advertises tool sets to the model.
func (r *Registry) AddToolSets(toolSets ...*aipb.ToolSet) {
	r.toolSets = append(r.toolSets, toolSets...)
}

// Tools returns the advertised tool definitions.
func (r *Registry) Tools() []*aipb.Tool {
	return r.tools
}

// ToolSets returns the advertised tool sets.
func (r *Registry) ToolSets() []*aipb.ToolSet {
	return r.toolSets
}

// Handles reports whether a tool is registered for the given call.
func (r *Registry) Handles(toolCall *aipb.ToolCall) bool {
	_, ok := r.handlerIDToTool[toolCall.GetAnnotations()[ToolHandlerIDAnnotation]]
	return ok
}

func (r *Registry) lookup(toolCall *aipb.ToolCall) (Tool, error) {
	handlerID := toolCall.GetAnnotations()[ToolHandlerIDAnnotation]
	tool, ok := r.handlerIDToTool[handlerID]
	if !ok {
		return nil, fmt.Errorf("no tool registered for %q (handler_id=%q)", toolCall.GetName(), handlerID)
	}
	return tool, nil
}

// Review dispatches to the tool's Review and persists the resulting metadata
// onto the call's annotations — the single place this happens.
func (r *Registry) Review(ctx context.Context, toolCall *aipb.ToolCall) (*sgptpb.ToolCallMetadata, error) {
	tool, err := r.lookup(toolCall)
	if err != nil {
		return nil, err
	}
	metadata, err := tool.Review(ctx, toolCall)
	if err != nil {
		return nil, fmt.Errorf("reviewing tool call %q: %w", toolCall.GetName(), err)
	}
	if err := SetToolCallMetadata(toolCall, metadata); err != nil {
		return nil, err
	}
	return metadata, nil
}

// Execute dispatches to the tool's Execute.
func (r *Registry) Execute(ctx context.Context, toolCall *aipb.ToolCall) (*aipb.ToolResult, error) {
	tool, err := r.lookup(toolCall)
	if err != nil {
		return nil, err
	}
	toolResult, err := tool.Execute(ctx, toolCall)
	if err != nil {
		return nil, fmt.Errorf("executing tool call %q: %w", toolCall.GetName(), err)
	}
	return toolResult, nil
}

// toolCallArgumentsJSON marshals a tool call's arguments to JSON bytes.
func toolCallArgumentsJSON(toolCall *aipb.ToolCall) ([]byte, error) {
	bytes, err := json.Marshal(toolCall.GetArguments().AsMap())
	if err != nil {
		return nil, fmt.Errorf("marshaling tool call arguments: %w", err)
	}
	return bytes, nil
}

// RequestRenderer is implemented by tools that dictate how their request
// renders in the timeline (e.g. edit_file renders a diff).
type RequestRenderer interface {
	Tool
	// RenderRequest returns markdown for the request; returning false falls
	// back to the default raw-JSON rendering.
	RenderRequest(toolCall *aipb.ToolCall) (string, bool)
}

// RenderRequest returns tool-provided request markdown for a call, if its
// tool implements RequestRenderer.
func (r *Registry) RenderRequest(toolCall *aipb.ToolCall) (string, bool) {
	tool, err := r.lookup(toolCall)
	if err != nil {
		return "", false
	}
	renderer, ok := tool.(RequestRenderer)
	if !ok {
		return "", false
	}
	return renderer.RenderRequest(toolCall)
}
