package toolengine

import (
	"context"
	"fmt"
	"sync"
	"time"

	aienginepb "github.com/malonaz/core/genproto/ai/ai_engine/v1"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"
	"github.com/malonaz/core/go/grpc"
	"github.com/malonaz/core/go/pbutil"
	"github.com/malonaz/core/go/pbutil/pbreflection"
	reflectionpb "google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/structpb"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/tools"
)

type engineConnection struct {
	client           aienginepb.AiEngineClient
	methodInvoker    *pbreflection.MethodInvoker
	reflectionClient reflectionpb.ServerReflectionClient
}

type Manager struct {
	mu                  sync.Mutex
	toolSets            []*aipb.ToolSet
	toolSetNameToEngine map[string]*engineConnection
	closers             []func()
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
		engine := &engineConnection{
			client:           aienginepb.NewAiEngineClient(conn.Get()),
			methodInvoker:    pbreflection.NewMethodInvoker(conn.Get()),
			reflectionClient: reflectionpb.NewServerReflectionClient(conn.Get()),
		}
		for _, request := range toolEngine.GetToolSets() {
			toolSet, err := engine.client.CreateServiceToolSet(ctx, request)
			if err != nil {
				return nil, err
			}
			manager.toolSetNameToEngine[toolSet.GetName()] = engine
			manager.toolSets = append(manager.toolSets, toolSet)
		}
	}

	return manager, nil
}

func (m *Manager) GetTools() []*aipb.Tool {
	m.mu.Lock()
	defer m.mu.Unlock()

	var allTools []*aipb.Tool
	for _, toolSet := range m.toolSets {
		discoveryTool := toolSet.DiscoveryTool
		if discoveryTool.Annotations == nil {
			discoveryTool.Annotations = map[string]string{}
		}
		discoveryTool.Annotations[tools.ToolHandlerIDAnnotation] = tools.HandlerIDEngine
		allTools = append(allTools, discoveryTool)
		for _, tool := range toolSet.Tools {
			if toolSet.ToolNameToDiscoverTimestamp[tool.GetName()] > 0 {
				if tool.Annotations == nil {
					tool.Annotations = map[string]string{}
				}
				tool.Annotations[tools.ToolHandlerIDAnnotation] = tools.HandlerIDEngine
				allTools = append(allTools, tool)
			}
		}
	}
	return allTools
}

func (m *Manager) MarkDiscovered(toolSetName string, toolNames []string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, toolSet := range m.toolSets {
		if toolSet.Name != toolSetName {
			continue
		}
		for _, toolName := range toolNames {
			toolSet.ToolNameToDiscoverTimestamp[toolName] = time.Now().UnixMicro()
		}
		return
	}
}

func (m *Manager) HandleToolCall(ctx context.Context, toolCall *aipb.ToolCall) (*tools.HandleResult, error) {
	m.mu.Lock()
	toolSets := make([]*aipb.ToolSet, len(m.toolSets))
	copy(toolSets, m.toolSets)

	var engine *engineConnection
	for _, e := range m.toolSetNameToEngine {
		engine = e
		break
	}
	m.mu.Unlock()

	if engine == nil {
		return nil, fmt.Errorf("no engine client available")
	}

	parseToolCallRequest := &aienginepb.ParseToolCallRequest{
		ToolCall: toolCall,
		ToolSets: toolSets,
	}
	parseToolCallResponse, err := engine.client.ParseToolCall(ctx, parseToolCallRequest)
	if err != nil {
		return nil, fmt.Errorf("parsing tool call: %w", err)
	}

	switch result := parseToolCallResponse.Result.(type) {
	case *aienginepb.ParseToolCallResponse_Discovery:
		return &tools.HandleResult{
			Display:     fmt.Sprintf("Discovering tools: %v", result.Discovery.ToolNames),
			AutoExecute: true,
		}, nil
	case *aienginepb.ParseToolCallResponse_Rpc:
		return &tools.HandleResult{
			Display:     fmt.Sprintf("RPC: %s", result.Rpc.MethodFullName),
			AutoExecute: false,
		}, nil
	default:
		return nil, fmt.Errorf("unknown parse result type: %T", result)
	}
}

func (m *Manager) ProcessToolCall(ctx context.Context, toolCall *aipb.ToolCall) (*aipb.ToolResult, error) {
	m.mu.Lock()
	toolSets := make([]*aipb.ToolSet, len(m.toolSets))
	copy(toolSets, m.toolSets)

	var engine *engineConnection
	for _, e := range m.toolSetNameToEngine {
		engine = e
		break
	}
	m.mu.Unlock()

	if engine == nil {
		return nil, fmt.Errorf("no engine client available")
	}

	parseToolCallRequest := &aienginepb.ParseToolCallRequest{
		ToolCall: toolCall,
		ToolSets: toolSets,
	}
	parseToolCallResponse, err := engine.client.ParseToolCall(ctx, parseToolCallRequest)
	if err != nil {
		return nil, fmt.Errorf("parsing tool call: %w", err)
	}

	switch result := parseToolCallResponse.Result.(type) {
	case *aienginepb.ParseToolCallResponse_Discovery:
		m.MarkDiscovered(result.Discovery.ToolSetName, result.Discovery.ToolNames)
		return ai.NewToolResult(toolCall.Name, toolCall.Id, "ok"), nil

	case *aienginepb.ParseToolCallResponse_Rpc:
		methodDescriptor, err := resolveMethodDescriptor(ctx, engine.reflectionClient, result.Rpc.MethodFullName)
		if err != nil {
			return nil, fmt.Errorf("resolving method %q: %w", result.Rpc.MethodFullName, err)
		}

		request := dynamicpb.NewMessage(methodDescriptor.Input())
		requestBytes, err := result.Rpc.Request.MarshalJSON()
		if err != nil {
			return nil, fmt.Errorf("marshaling request: %w", err)
		}
		if err := pbutil.JSONUnmarshal(requestBytes, request); err != nil {
			return nil, fmt.Errorf("unmarshaling request: %w", err)
		}

		response, err := engine.methodInvoker.Invoke(ctx, methodDescriptor, request)
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

	default:
		return nil, fmt.Errorf("unknown parse result type: %T", result)
	}
}

func resolveMethodDescriptor(ctx context.Context, reflectionClient reflectionpb.ServerReflectionClient, methodFullName string) (protoreflect.MethodDescriptor, error) {
	schema, err := pbreflection.ResolveSchema(ctx, reflectionClient)
	if err != nil {
		return nil, fmt.Errorf("resolving schema: %w", err)
	}
	descriptor, err := schema.FindDescriptorByName(protoreflect.FullName(methodFullName))
	if err != nil {
		return nil, fmt.Errorf("finding descriptor: %w", err)
	}
	methodDescriptor, ok := descriptor.(protoreflect.MethodDescriptor)
	if !ok {
		return nil, fmt.Errorf("expected method descriptor for %q, got %T", methodFullName, descriptor)
	}
	return methodDescriptor, nil
}

func (m *Manager) HasToolSets() bool {
	return len(m.toolSets) > 0
}

func (m *Manager) Close() {
	for _, closer := range m.closers {
		closer()
	}
	m.closers = nil
}

var _ tools.Handler = (*Manager)(nil)
