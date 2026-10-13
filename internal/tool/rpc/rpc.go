package rpc

import (
	"context"
	"crypto/sha256"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

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
	"google.golang.org/protobuf/proto"
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

// toolSetCacheKey keys a tool set by its engine and request: a changed
// request (e.g. a schema configuration) must not be served a stale schema.
func toolSetCacheKey(engineName string, request *aienginepb.CreateServiceToolSetRequest) (string, error) {
	requestBytes, err := pbutil.MarshalDeterministic(request)
	if err != nil {
		return "", fmt.Errorf("marshaling tool set request: %w", err)
	}
	// Engine names are selectors ("//dir:title", "@import//dir:title");
	// flatten them into a safe file name.
	sanitized := strings.NewReplacer("/", "_", ":", "_", "@", "_").Replace(engineName)
	return fmt.Sprintf("%s%s_%x.pb", toolSetCacheKeyPrefix, sanitized, sha256.Sum256(requestBytes)), nil
}

// withTitles returns the request with every schema configuration injecting
// the tool call title, unless the configuration sets its own.
func withTitles(request *aienginepb.CreateServiceToolSetRequest) *aienginepb.CreateServiceToolSetRequest {
	request = proto.CloneOf(request)
	if request.SchemaConfiguration == nil {
		request.SchemaConfiguration = &aienginepb.SchemaConfiguration{}
	}
	schemaConfigurations := append(slices.Collect(maps.Values(request.GetMethodNameToSchemaConfiguration())), request.SchemaConfiguration)
	for _, schemaConfiguration := range schemaConfigurations {
		if schemaConfiguration.GetWithTitle() == "" {
			schemaConfiguration.WithTitle = tool.TitleDescription
		}
	}
	return request
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
	for _, request := range toolEngine.GetToolSets() {
		request = withTitles(request)
		cacheKey, err := toolSetCacheKey(toolEngine.GetName(), request)
		if err != nil {
			return nil, err
		}

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
	toolSetName, ok := aip.GetAnnotation(toolCall, aipb.Annotations.ToolSetName.Key)
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

	toolType, _ := aip.GetAnnotation(toolCall, aipb.Annotations.ToolType.Key)
	switch toolType {
	case aitool.ToolTypeDiscovery:
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

	case aitool.ToolTypeGenerateRPCRequest:
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
	// Any payloads reference types only the reflected server knows.
	resolver := pbutil.WithResolver(engine.schema.Types())
	if err := pbutil.JSONUnmarshal(requestBytes, request, resolver); err != nil {
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

	responseBytes, err := pbutil.JSONMarshal(response, resolver)
	if err != nil {
		return nil, fmt.Errorf("marshaling response: %w", err)
	}
	value := &structpb.Value{}
	if err := value.UnmarshalJSON(responseBytes); err != nil {
		return nil, fmt.Errorf("unmarshaling response into structpb.Value: %w", err)
	}
	return ai.NewStructuredToolResult(toolCall.Name, toolCall.Id, value), nil
}

// Summarize reads the call's annotations and arguments directly instead of
// parsing it against the schema: it runs on every render.
func (m *Manager) Summarize(toolCall *aipb.ToolCall, toolResult *aipb.ToolResult) (tool.Summary, bool) {
	switch tool.GetToolCallAnnotation(toolCall, aipb.Annotations.ToolType.Key) {
	case aitool.ToolTypeDiscovery:
		return summarizeDiscovery(toolCall, toolResult), true
	case aitool.ToolTypeGenerateRPCRequest:
		return summarizeRPC(toolCall, toolResult), true
	}
	return tool.Summary{}, false
}

// summarizeDiscovery: "Discover" the tool set, then the methods it found.
func summarizeDiscovery(toolCall *aipb.ToolCall, toolResult *aipb.ToolResult) tool.Summary {
	toolSetName := tool.GetToolCallAnnotation(toolCall, aipb.Annotations.ToolSetName.Key)
	summary := tool.Summary{Verb: "Discover", Subject: toolSetName[strings.LastIndex(toolSetName, "/")+1:]}
	if toolResult == nil {
		return summary
	}
	discovered, _ := aip.GetAnnotation(toolResult, aipb.Annotations.DiscoveredTools.Key)
	if discovered == "" {
		summary.Outcome = "no tools"
		return summary
	}
	names := strings.Split(discovered, ",")
	methods := make([]string, 0, len(names))
	for _, name := range names {
		// Discovered tools are named {Service}_{Method}.
		methods = append(methods, name[strings.IndexByte(name, '_')+1:])
	}
	summary.Subject = strings.Join(methods, ", ")
	summary.Outcome = tool.Count(len(names), "tool", "tools")
	return summary
}

// summarizeRPC: the method as a verb ("List contacts"), the resource or most
// identifying argument as the subject, and the size of a listed response.
func summarizeRPC(toolCall *aipb.ToolCall, toolResult *aipb.ToolResult) tool.Summary {
	method := tool.GetToolCallAnnotation(toolCall, aipb.Annotations.GrpcMethod.Key)
	summary := tool.Summary{Verb: humanize(method[strings.LastIndexAny(method, "./")+1:])}
	if summary.Verb == "" {
		summary.Verb = toolCall.GetName()
	}
	summary.Subject = identifyingArgument(toolCall.GetArguments().GetFields())
	if toolResult == nil {
		return summary
	}
	// A listed response is summarized by its size; the first list in key
	// order keeps the outcome stable across renders.
	fields := toolResult.GetStructuredContent().GetStructValue().GetFields()
	listKey := ""
	for key, value := range fields {
		if value.GetListValue() != nil && (listKey == "" || key < listKey) {
			listKey = key
		}
	}
	if listKey != "" {
		summary.Outcome = fmt.Sprintf("%d %s", len(fields[listKey].GetListValue().GetValues()), strings.ReplaceAll(humanize(listKey), "_", " "))
	}
	return summary
}

// identifyingArgument picks the argument that best names what a request acts
// on: an AIP resource name, then a parent. The title is shown separately.
func identifyingArgument(fields map[string]*structpb.Value) string {
	// Models sometimes wrap the request in an {"arguments": {...}} envelope.
	if envelope := fields["arguments"].GetStructValue(); envelope != nil {
		fields = envelope.GetFields()
	}
	for _, key := range []string{"name", "parent"} {
		if value := fields[key].GetStringValue(); value != "" {
			return value
		}
	}
	return ""
}

// humanize splits a camel-case identifier into a sentence-case phrase,
// keeping acronyms: "ListAPIKeys" reads "List API keys".
func humanize(identifier string) string {
	runes := []rune(identifier)
	var words []string
	start := 0
	for i := 1; i <= len(runes); i++ {
		// A word ends before an upper-case letter that follows a lower-case
		// one, or that starts a word after an acronym ("APIKeys").
		if i < len(runes) && !(unicode.IsUpper(runes[i]) &&
			(unicode.IsLower(runes[i-1]) || i+1 < len(runes) && unicode.IsLower(runes[i+1]) && unicode.IsUpper(runes[i-1]))) {
			continue
		}
		word := string(runes[start:i])
		if len(words) > 0 && strings.ToUpper(word) != word {
			word = strings.ToLower(word)
		}
		words = append(words, word)
		start = i
	}
	return strings.Join(words, " ")
}

// Close tears down all engine connections.
func (m *Manager) Close() {
	for _, closer := range m.closers {
		closer()
	}
	m.closers = nil
}

var (
	_ tool.Tool       = (*Manager)(nil)
	_ tool.Summarizer = (*Manager)(nil)
)
