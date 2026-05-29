package toolengine

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
	"github.com/malonaz/sgpt/internal/debug"
	"github.com/malonaz/sgpt/internal/tools"
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

type Manager struct {
	mu                  sync.Mutex
	toolSets            []*aipb.ToolSet
	toolSetNameToEngine map[string]*engineConnection
	closers             []func()
}

func toolSetCacheKey(engineName string, index int) string {
	return fmt.Sprintf("%s%s_%d.pb", toolSetCacheKeyPrefix, engineName, index)
}

func Initialize(
	ctx context.Context,
	config *sgptpb.Configuration,
	baseURLToGRPCConnection map[string]*grpc.Connection,
) (*Manager, error) {
	manager := &Manager{
		toolSetNameToEngine: map[string]*engineConnection{},
	}

	for _, toolEngine := range config.GetToolEngines() {
		conn := baseURLToGRPCConnection[toolEngine.GetEngineService().GetBaseUrl()]
		reflectionClient := reflectionpb.NewServerReflectionClient(conn.Get())

		// Resolve and cache schema per engine.
		cacheKey := toolEngine.GetEngineService().GetBaseUrl()
		cacheDir := cache.Dir()
		schema, err := pbreflection.ResolveSchema(ctx, reflectionClient,
			pbreflection.WithDiskCache(cacheKey, cacheDir, schemaCacheMaxAge),
		)
		if err != nil {
			return nil, fmt.Errorf("resolving schema for %s: %w", toolEngine.GetName(), err)
		}

		engine := &engineConnection{
			client:           aienginepb.NewAiEngineClient(conn.Get()),
			methodInvoker:    pbreflection.NewMethodInvoker(conn.Get()),
			reflectionClient: reflectionClient,
			schema:           schema,
			schemaBuilder:    pbjson.NewSchemaBuilder(schema),
		}
		for i, request := range toolEngine.GetToolSets() {
			cacheKey := toolSetCacheKey(toolEngine.GetName(), i)

			cachedToolSet, ok := cache.Get(cacheKey, toolSetCacheMaxAge, &aipb.ToolSet{})
			if ok && cachedToolSet.GetName() != "" {
				manager.toolSetNameToEngine[cachedToolSet.GetName()] = engine
				manager.toolSets = append(manager.toolSets, cachedToolSet)
				continue
			}

			toolSet, err := engine.client.CreateServiceToolSet(ctx, request)
			if err != nil {
				return nil, err
			}
			aip.SetAnnotation(toolSet.DiscoveryTool, tools.ToolHandlerIDAnnotation, tools.HandlerIDEngine)
			for _, tool := range toolSet.GetTools() {
				aip.SetAnnotation(tool, tools.ToolHandlerIDAnnotation, tools.HandlerIDEngine)
			}
			cache.Store(cacheKey, toolSet)
			manager.toolSetNameToEngine[toolSet.GetName()] = engine
			debug.Log(toolSet.GetName())
			manager.toolSets = append(manager.toolSets, toolSet)
		}
	}
	return manager, nil
}

func (m *Manager) GetToolSets() []*aipb.ToolSet {
	if m == nil {
		return nil
	}
	return m.toolSets
}

func (m *Manager) HandleToolCall(ctx context.Context, toolCall *aipb.ToolCall) (*tools.HandleResult, error) {
	toolSetName, ok := aip.GetAnnotation(toolCall, aitool.AnnotationKeyToolSetName)
	if !ok {
		return nil, fmt.Errorf("no tool set annotation found on tool call")
	}
	engine, ok := m.toolSetNameToEngine[toolSetName]
	if !ok {
		return nil, fmt.Errorf("no engine found for tool set %q", toolSetName)
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
		var displayToolNames []string
		if discovered, ok := aip.GetAnnotation(toolResult, aitool.AnnotationKeyDiscoveredTools); ok && discovered != "" {
			displayToolNames = strings.Split(discovered, ",")
		}
		displayContent := "Discovered tools"
		if len(displayToolNames) > 0 {
			displayContent = fmt.Sprintf("Discovered %s", strings.Join(displayToolNames, ", "))
		}
		parsedResult, err := ai.ParseToolResult(toolResult)
		if err != nil {
			return nil, fmt.Errorf("parsing discovery tool result: %w", err)
		}
		if toolResult.GetError() != nil {
			displayContent += fmt.Sprintf(" (errors: %s)", parsedResult)
		}
		toolCallMetadata.DisplayMessage.Content = displayContent
		toolCallMetadata.AutoExecute = true

	case aitool.AnnotationValueToolTypeGenerateRPCRequest:
		parseToolCallResponse, err := aitool.ParseToolCall(engine.schemaBuilder, toolCall, m.toolSets)
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
		toolCallMetadata.AutoExecute = methodOptions.GetIdempotencyLevel() == descriptorpb.MethodOptions_NO_SIDE_EFFECTS
	default:
		return nil, fmt.Errorf("unknown tool type: %s", toolType)
	}

	if err := tools.SetToolCallMetadata(toolCall, toolCallMetadata); err != nil {
		return nil, fmt.Errorf("annotating tool call: %w", err)
	}
	debug.LogProto("toolcallmetadsata", toolCallMetadata)
	return &tools.HandleResult{
		Display:     toolCallMetadata.DisplayMessage.Content,
		AutoExecute: toolCallMetadata.AutoExecute,
	}, nil
}

func (m *Manager) ProcessToolCall(ctx context.Context, toolCall *aipb.ToolCall) (*aipb.ToolResult, error) {
	toolSetName, ok := aip.GetAnnotation(toolCall, aitool.AnnotationKeyToolSetName)
	if !ok {
		return nil, fmt.Errorf("no tool set annotation found on tool call")
	}
	engine, ok := m.toolSetNameToEngine[toolSetName]
	if !ok {
		return nil, fmt.Errorf("no engine found for tool set %q", toolSetName)
	}

	parseToolCallResponse, err := aitool.ParseToolCall(engine.schemaBuilder, toolCall, m.toolSets)
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

func (m *Manager) Close() {
	for _, closer := range m.closers {
		closer()
	}
	m.closers = nil
}

var _ tools.Handler = (*Manager)(nil)
