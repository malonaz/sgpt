package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	aipb "github.com/malonaz/core/genproto/ai/v1"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
)

// ToolHandlerIDAnnotation routes a tool call to its registered Tool.
const ToolHandlerIDAnnotation = "sgpt.com/tool-handler-id"

const (
	HandlerIDShell     = "shell"
	HandlerIDReadFiles = "read_files"
	HandlerIDEngine    = "engine"
	HandlerIDDiff      = "diff"
	HandlerIDReplace   = "replace"
	HandlerIDAgent     = "agent"
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
// Guarded by a mutex: tools/tool sets are registered from background
// goroutines (e.g. MCP discovery) while the TUI reads them on every render.
type Registry struct {
	mutex           sync.RWMutex
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
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.handlerIDToTool[handlerID] = tool
}

// AddTools advertises tool definitions to the model.
func (r *Registry) AddTools(tools ...*aipb.Tool) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.tools = append(r.tools, tools...)
}

// AddToolSets advertises tool sets to the model.
func (r *Registry) AddToolSets(toolSets ...*aipb.ToolSet) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.toolSets = append(r.toolSets, toolSets...)
}

// Tools returns the advertised tool definitions.
func (r *Registry) Tools() []*aipb.Tool {
	r.mutex.RLock()
	defer r.mutex.RUnlock()
	// Copy: callers must not observe later appends re-slicing under them.
	tools := make([]*aipb.Tool, len(r.tools))
	copy(tools, r.tools)
	return tools
}

// ToolSets returns the advertised tool sets.
func (r *Registry) ToolSets() []*aipb.ToolSet {
	r.mutex.RLock()
	defer r.mutex.RUnlock()
	toolSets := make([]*aipb.ToolSet, len(r.toolSets))
	copy(toolSets, r.toolSets)
	return toolSets
}

// Handles reports whether a tool is registered for the given call.
func (r *Registry) Handles(toolCall *aipb.ToolCall) bool {
	// Locked read: the session goroutine mutates annotations concurrently.
	handlerID := GetToolCallAnnotation(toolCall, ToolHandlerIDAnnotation)
	r.mutex.RLock()
	defer r.mutex.RUnlock()
	_, ok := r.handlerIDToTool[handlerID]
	return ok
}

func (r *Registry) lookup(toolCall *aipb.ToolCall) (Tool, error) {
	// Locked read: the session goroutine mutates annotations concurrently.
	handlerID := GetToolCallAnnotation(toolCall, ToolHandlerIDAnnotation)
	r.mutex.RLock()
	tool, ok := r.handlerIDToTool[handlerID]
	r.mutex.RUnlock()
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

// ArgumentsJSON marshals a tool call's arguments to JSON bytes.
func ArgumentsJSON(toolCall *aipb.ToolCall) ([]byte, error) {
	bytes, err := json.Marshal(toolCall.GetArguments().AsMap())
	if err != nil {
		return nil, fmt.Errorf("marshaling tool call arguments: %w", err)
	}
	return bytes, nil
}

// RequestRenderer is implemented by tools that dictate how their request
// renders in the timeline (e.g. the diff tool renders a diff).
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

// HeaderRenderer is implemented by tools that render their calls' header in
// the timeline as markdown (e.g. the diff tool shows the edited file).
type HeaderRenderer interface {
	Tool
	// RenderHeader returns header markdown; returning false falls back to
	// the default tool-name rendering.
	RenderHeader(toolCall *aipb.ToolCall) (string, bool)
}

// RenderHeader returns tool-provided header markdown for a call, if its tool
// implements HeaderRenderer.
func (r *Registry) RenderHeader(toolCall *aipb.ToolCall) (string, bool) {
	tool, err := r.lookup(toolCall)
	if err != nil {
		return "", false
	}
	renderer, ok := tool.(HeaderRenderer)
	if !ok {
		return "", false
	}
	return renderer.RenderHeader(toolCall)
}
