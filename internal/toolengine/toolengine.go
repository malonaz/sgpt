// internal/toolengine/toolengine.go
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
	"github.com/malonaz/core/go/logging"
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
	client        aienginepb.AiEngineClient
	methodInvoker *pbreflection.MethodInvoker
	schema        *pbreflection.Schema
}

type Manager struct {
	mu                  sync.Mutex
	toolSets            []*aipb.ToolSet
	toolSetNameToEngine map[string]*engineConnection
	closers             []func()
}

func Initialize(ctx context.Context, configurations []*sgptpb.ToolEngineConfiguration) (*Manager, error) {
	if len(configurations) == 0 {
		return &Manager{toolSetNameToEngine: map[string]*engineConnection{}}, nil
	}

	errorLogger, err := logging.NewLogger(&logging.Opts{
		Format: "pretty",
		Level:  "error",
	})
	if err != nil {
		return nil, err
	}

	manager := &Manager{
		toolSetNameToEngine: map[string]*engineConnection{},
	}

	for _, engineConfiguration := range configurations {
		grpcConfig := engineConfiguration.EngineService
		opts, err := grpc.ParseOpts(grpcConfig.BaseUrl)
		if err != nil {
			return nil, err
		}
		connection, err := grpc.NewConnection(opts, nil, nil)
		if err != nil {
			manager.Close()
			return nil, fmt.Errorf("creating connection for engine %q: %w", engineConfiguration.Name, err)
		}
		connection.WithLogger(errorLogger)
		connection.WithMetadata(grpcConfig.ApiKeyHeader, grpcConfig.ApiKey)

		if err := connection.Connect(ctx); err != nil {
			manager.Close()
			return nil, fmt.Errorf("connecting to engine %q: %w", engineConfiguration.Name, err)
		}

		manager.closers = append(manager.closers, func() { connection.Close() })

		reflectionClient := reflectionpb.NewServerReflectionClient(connection.Get())
		schema, err := pbreflection.ResolveSchema(ctx, reflectionClient)
		if err != nil {
			manager.Close()
			return nil, fmt.Errorf("resolving schema for engine %q: %w", engineConfiguration.Name, err)
		}

		engine := &engineConnection{
			client:        aienginepb.NewAiEngineClient(connection.Get()),
			methodInvoker: pbreflection.NewMethodInvoker(connection.Get()),
			schema:        schema,
		}

		for _, request := range engineConfiguration.ToolSets {
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
		descriptor, err := engine.schema.FindDescriptorByName(protoreflect.FullName(result.Rpc.MethodFullName))
		if err != nil {
			return nil, fmt.Errorf("method not found %q: %w", result.Rpc.MethodFullName, err)
		}
		methodDescriptor, ok := descriptor.(protoreflect.MethodDescriptor)
		if !ok {
			return nil, fmt.Errorf("expected method descriptor for %q, got %T", result.Rpc.MethodFullName, descriptor)
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
