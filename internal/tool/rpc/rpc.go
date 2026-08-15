package rpc

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	aienginepb "github.com/malonaz/core/genproto/ai/ai_engine/v1"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"
	aitool "github.com/malonaz/core/go/ai/tool"
	"github.com/malonaz/core/go/aip"
	"github.com/malonaz/core/go/grpc"
	"github.com/malonaz/core/go/grpc/middleware"
	"github.com/malonaz/core/go/pbutil"
	"github.com/malonaz/core/go/pbutil/pbfieldmask"
	"github.com/malonaz/core/go/pbutil/pbjson"
	"github.com/malonaz/core/go/pbutil/pbreflection"
	reflectionpb "google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/protobuf/reflect/protoreflect"
	descriptorpb "google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/structpb"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/cache"
	"github.com/malonaz/sgpt/internal/configuration"
	"github.com/malonaz/sgpt/internal/debug"
	"github.com/malonaz/sgpt/internal/tool"
)

const (
	toolSetCacheKeyPrefix = "toolset_"
	toolSetCacheMaxAge    = 24 * time.Hour
	schemaCacheMaxAge     = 24 * time.Hour
)

type engineConnection struct {
	client           aienginepb.AiEngineClient
	methodInvoker    *pbreflection.MethodInvoker
	reflectionClient reflectionpb.ServerReflectionClient
	schema           *pbreflection.Schema
	schemaBuilder    *pbjson.SchemaBuilder
}

// Manager connects to remote tool engines and implements tool.Tool for
// the tool sets they expose.
type Manager struct {
	configuration              *sgptpb.Configuration
	clientNameToGRPCConnection map[string]*grpc.Connection
	// engineNameToConfiguration indexes the discovered tool set files
	// (selector-named) this manager can initialize.
	engineNameToConfiguration map[string]*sgptpb.ToolSet

	mu                  sync.Mutex
	toolSets            []*aipb.ToolSet
	toolSetNameToEngine map[string]*engineConnection
	// engineNameToToolSets records initialized engines: engines are dialed
	// lazily, on the first EnsureEngine call for their name.
	engineNameToToolSets map[string][]*aipb.ToolSet
	closers              []func()
}

func toolSetCacheKey(engineName string, index int) string {
	// Engine names are selectors ("//dir:title", "@import//dir:title");
	// flatten them into a safe file name.
	sanitized := strings.NewReplacer("/", "_", ":", "_", "@", "_").Replace(engineName)
	return fmt.Sprintf("%s%s_%d.pb", toolSetCacheKeyPrefix, sanitized, index)
}

// NewManager creates a lazy manager: no engine is contacted until
// EnsureEngine is called for it.
func NewManager(
	configuration *sgptpb.Configuration,
	clientNameToGRPCConnection map[string]*grpc.Connection,
	toolSetConfigurations []*sgptpb.ToolSet,
) *Manager {
	engineNameToConfiguration := map[string]*sgptpb.ToolSet{}
	for _, toolSetConfiguration := range toolSetConfigurations {
		engineNameToConfiguration[toolSetConfiguration.GetName()] = toolSetConfiguration
	}
	return &Manager{
		configuration:              configuration,
		clientNameToGRPCConnection: clientNameToGRPCConnection,
		engineNameToConfiguration:  engineNameToConfiguration,
		toolSetNameToEngine:        map[string]*engineConnection{},
		engineNameToToolSets:       map[string][]*aipb.ToolSet{},
	}
}

// EnsureEngine initializes the named engine on first use (schema resolution
// + tool set creation) and returns its tool sets; later calls are served
// from memory.
func (m *Manager) EnsureEngine(ctx context.Context, engineName string) ([]*aipb.ToolSet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if toolSets, ok := m.engineNameToToolSets[engineName]; ok {
		return toolSets, nil
	}
	toolEngine, ok := m.engineNameToConfiguration[engineName]
	if !ok {
		return nil, fmt.Errorf("unknown tool engine %q", engineName)
	}

	engineService, err := configuration.GrpcClient(m.configuration, toolEngine.GetEngineService())
	if err != nil {
		return nil, err
	}
	connection := m.clientNameToGRPCConnection[engineService.GetName()]
	reflectionClient := reflectionpb.NewServerReflectionClient(connection.Get())
	// Resolve and cache the schema for this engine.
	schema, err := pbreflection.ResolveSchema(ctx, reflectionClient,
		pbreflection.WithDiskCache(engineService.GetBaseUrl(), cache.Dir(), schemaCacheMaxAge),
	)
	if err != nil {
		return nil, fmt.Errorf("resolving schema for %s: %w", toolEngine.GetName(), err)
	}

	engine := &engineConnection{
		client:           aienginepb.NewAiEngineClient(connection.Get()),
		methodInvoker:    pbreflection.NewMethodInvoker(connection.Get()),
		reflectionClient: reflectionClient,
		schema:           schema,
		schemaBuilder:    pbjson.NewSchemaBuilder(schema),
	}
	var toolSets []*aipb.ToolSet
	for i, request := range toolEngine.GetToolSets() {
		cacheKey := toolSetCacheKey(toolEngine.GetName(), i)

		cachedToolSet, ok := cache.Get(cacheKey, toolSetCacheMaxAge, &aipb.ToolSet{})
		if ok && cachedToolSet.GetName() != "" {
			m.toolSetNameToEngine[cachedToolSet.GetName()] = engine
			toolSets = append(toolSets, cachedToolSet)
			continue
		}

		toolSet, err := engine.client.CreateServiceToolSet(ctx, request)
		if err != nil {
			return nil, err
		}
		aip.SetAnnotation(toolSet.DiscoveryTool, tool.ToolHandlerIDAnnotation, tool.HandlerIDEngine)
		for _, engineTool := range toolSet.GetTools() {
			aip.SetAnnotation(engineTool, tool.ToolHandlerIDAnnotation, tool.HandlerIDEngine)
		}
		cache.Store(cacheKey, toolSet)
		m.toolSetNameToEngine[toolSet.GetName()] = engine
		toolSets = append(toolSets, toolSet)
	}
	m.engineNameToToolSets[engineName] = toolSets
	m.toolSets = append(m.toolSets, toolSets...)
	return toolSets, nil
}

// GetToolSets returns the tool sets of every initialized engine.
func (m *Manager) GetToolSets() []*aipb.ToolSet {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	toolSets := make([]*aipb.ToolSet, len(m.toolSets))
	copy(toolSets, m.toolSets)
	return toolSets
}

func (m *Manager) engineFor(toolCall *aipb.ToolCall) (*engineConnection, error) {
	toolSetName, ok := aip.GetAnnotation(toolCall, aitool.AnnotationKeyToolSetName)
	if !ok {
		return nil, fmt.Errorf("no tool set annotation found on tool call")
	}
	// Locked: engines register concurrently (lazy init off the UI loop).
	m.mu.Lock()
	engine, ok := m.toolSetNameToEngine[toolSetName]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("no engine found for tool set %q", toolSetName)
	}
	return engine, nil
}

// Review implements tool.Tool.
func (m *Manager) Review(ctx context.Context, toolCall *aipb.ToolCall) (*sgptpb.ToolCallMetadata, error) {
	engine, err := m.engineFor(toolCall)
	if err != nil {
		return nil, err
	}

	toolCallMetadata := &sgptpb.ToolCallMetadata{
		DisplayMessage: &sgptpb.DisplayMessage{},
	}

	toolType, _ := aip.GetAnnotation(toolCall, aitool.AnnotationKeyToolType)
	switch toolType {
	case aitool.AnnotationValueToolTypeDiscovery:
		toolResult := toolCall.GetResult()
		if toolResult == nil {
			return nil, fmt.Errorf("discovery tool call %q has no result", toolCall.GetName())
		}
		parsedResult, err := ai.ParseToolResult(toolResult)
		if err != nil {
			return nil, fmt.Errorf("parsing discovery tool result: %w", err)
		}
		if toolResult.GetError() != nil {
			toolCallMetadata.DisplayMessage.Content = fmt.Sprintf("error: %s", parsedResult)
		}
		toolCallMetadata.AutoExecute = true

	case aitool.AnnotationValueToolTypeGenerateRPCRequest:
		parseToolCallResponse, err := aitool.ParseToolCall(engine.schemaBuilder, toolCall, m.GetToolSets())
		if err != nil {
			return nil, err
		}
		rpc := parseToolCallResponse.GetRpc()
		descriptor, err := engine.schema.FindDescriptorByName(protoreflect.FullName(rpc.MethodFullName))
		if err != nil {
			return nil, fmt.Errorf("finding descriptor %q: %w", rpc.MethodFullName, err)
		}
		methodDescriptor, ok := descriptor.(protoreflect.MethodDescriptor)
		if !ok {
			return nil, fmt.Errorf("expected method descriptor, got %T", descriptor)
		}
		methodOptions, ok := methodDescriptor.Options().(*descriptorpb.MethodOptions)
		if !ok {
			return nil, fmt.Errorf("expected method options for %q, got %T", rpc.MethodFullName, methodDescriptor.Options())
		}
		// Side-effect-free RPCs are safe to run without user confirmation.
		toolCallMetadata.AutoExecute = methodOptions.GetIdempotencyLevel() == descriptorpb.MethodOptions_NO_SIDE_EFFECTS
	default:
		return nil, fmt.Errorf("unknown tool type: %s", toolType)
	}

	debug.LogProto("tool call metadata", toolCallMetadata)
	return toolCallMetadata, nil
}

// Execute implements tool.Tool.
func (m *Manager) Execute(ctx context.Context, toolCall *aipb.ToolCall) (*aipb.ToolResult, error) {
	engine, err := m.engineFor(toolCall)
	if err != nil {
		return nil, err
	}

	parseToolCallResponse, err := aitool.ParseToolCall(engine.schemaBuilder, toolCall, m.GetToolSets())
	if err != nil {
		return nil, err
	}

	rpc := parseToolCallResponse.GetRpc()
	if rpc == nil {
		return nil, fmt.Errorf("expected RPC parse result, got %T", parseToolCallResponse.Result)
	}

	descriptor, err := engine.schema.FindDescriptorByName(protoreflect.FullName(rpc.MethodFullName))
	if err != nil {
		return nil, fmt.Errorf("finding method descriptor %q: %w", rpc.MethodFullName, err)
	}
	methodDescriptor, ok := descriptor.(protoreflect.MethodDescriptor)
	if !ok {
		return nil, fmt.Errorf("expected method descriptor for %q, got %T", rpc.MethodFullName, descriptor)
	}

	request := dynamicpb.NewMessage(methodDescriptor.Input())
	requestBytes, err := rpc.Request.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}
	if err := pbutil.JSONUnmarshal(requestBytes, request); err != nil {
		return nil, fmt.Errorf("unmarshaling request: %w", err)
	}

	ctxInvoke := ctx
	if rpc.GetReadMask() != nil {
		ctxInvoke = middleware.WithReadMaskStrict(ctxInvoke, pbfieldmask.New(rpc.GetReadMask()).String())
	}
	response, err := engine.methodInvoker.Invoke(ctxInvoke, methodDescriptor, request)
	if err != nil {
		return ai.NewErrorToolResult(toolCall.Name, toolCall.Id, err), nil
	}

	responseBytes, err := pbutil.JSONMarshal(response)
	if err != nil {
		return nil, fmt.Errorf("marshaling response: %w", err)
	}
	value := &structpb.Value{}
	if err := value.UnmarshalJSON(responseBytes); err != nil {
		return nil, fmt.Errorf("unmarshaling response into structpb.Value: %w", err)
	}
	return ai.NewStructuredToolResult(toolCall.Name, toolCall.Id, value), nil
}

// RenderHeader shows {Service}/{Method} for RPC calls and a discrete label
// for discovery calls, instead of the generated tool names.
func (m *Manager) RenderHeader(toolCall *aipb.ToolCall) (string, bool) {
	toolType, _ := aip.GetAnnotation(toolCall, aitool.AnnotationKeyToolType)
	switch toolType {
	case aitool.AnnotationValueToolTypeDiscovery:
		toolResult := toolCall.GetResult()
		if toolResult == nil {
			return "🔍 discovering tools…", true
		}
		discovered, ok := aip.GetAnnotation(toolResult, aitool.AnnotationKeyDiscoveredTools)
		if !ok || discovered == "" {
			return "🔍 discovered tools", true
		}
		serviceToMethods := map[string][]string{}
		for _, name := range strings.Split(discovered, ",") {
			parts := strings.SplitN(name, "_", 2)
			if len(parts) == 2 {
				serviceToMethods[parts[0]] = append(serviceToMethods[parts[0]], parts[1])
			} else {
				serviceToMethods[""] = append(serviceToMethods[""], name)
			}
		}
		var sections []string
		for service, methods := range serviceToMethods {
			if service == "" {
				sections = append(sections, strings.Join(methods, ", "))
			} else {
				sections = append(sections, fmt.Sprintf("%s: discovered %s", service, strings.Join(methods, ", ")))
			}
		}
		return "🔍 " + strings.Join(sections, " | "), true
	case aitool.AnnotationValueToolTypeGenerateRPCRequest:
		engine, err := m.engineFor(toolCall)
		if err != nil {
			return "", false
		}
		// Parse a snapshot: this runs on the TUI render goroutine and
		// ParseToolCall walks the annotation map directly, racing with the
		// session goroutine's status/metadata writes.
		// Partial/unparsable arguments simply fall back to the default header.
		parseToolCallResponse, err := aitool.ParseToolCall(engine.schemaBuilder, tool.SnapshotToolCall(toolCall), m.GetToolSets())
		if err != nil {
			return "", false
		}
		parts := strings.Split(parseToolCallResponse.GetRpc().GetMethodFullName(), ".")
		if len(parts) < 2 {
			return "", false
		}
		return fmt.Sprintf("📡 `%s/%s`", parts[len(parts)-2], parts[len(parts)-1]), true
	}
	return "", false
}

// Close tears down all engine connections.
func (m *Manager) Close() {
	for _, closer := range m.closers {
		closer()
	}
	m.closers = nil
}

var (
	_ tool.Tool           = (*Manager)(nil)
	_ tool.HeaderRenderer = (*Manager)(nil)
)
