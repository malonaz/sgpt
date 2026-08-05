package tool

import (
	"context"
	_ "embed"
	"fmt"
	"sync"

	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"
	aitool "github.com/malonaz/core/go/ai/tool"
	"github.com/malonaz/core/go/pbutil"
	"github.com/malonaz/core/go/pbutil/pbjson"
	"github.com/malonaz/core/go/pbutil/pbreflection"
	"google.golang.org/grpc/reflection"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/structpb"
)

// toolServiceFullName anchors reflection resolution: only files reachable
// from a listed service are fetched.
const toolServiceFullName = "sgpt.v1.ToolService"

// The descriptor set is compiled with source info so proto comments become
// tool and field descriptions.
//
//go:embed descriptor_set.bin
var descriptorSetBytes []byte

var (
	schemaOnce sync.Once
	schema     *pbreflection.Schema
	schemaErr  error
)

// Schema returns the sgpt/v1 reflection schema, built once from the
// descriptor set embedded at compile time.
func Schema() (*pbreflection.Schema, error) {
	schemaOnce.Do(func() {
		schema, schemaErr = loadSchema()
	})
	return schema, schemaErr
}

func loadSchema() (*pbreflection.Schema, error) {
	fileDescriptorSet := &descriptorpb.FileDescriptorSet{}
	if err := pbutil.Unmarshal(descriptorSetBytes, fileDescriptorSet); err != nil {
		return nil, fmt.Errorf("unmarshaling embedded descriptor set: %w", err)
	}
	files, err := protodesc.NewFiles(fileDescriptorSet)
	if err != nil {
		return nil, fmt.Errorf("building file registry: %w", err)
	}
	types, err := pbreflection.NewTypesFromFiles(files)
	if err != nil {
		return nil, fmt.Errorf("building type registry: %w", err)
	}
	serviceInfoProvider, err := pbreflection.NewServiceInfoProvider(files, []string{toolServiceFullName})
	if err != nil {
		return nil, fmt.Errorf("building service info provider: %w", err)
	}
	// In-proc reflection server over the embedded descriptors — same path the
	// ai_engine uses for --file-descriptor-set, no network involved.
	reflectionClient := pbreflection.NewServerReflectionClientInProc(reflection.ServerOptions{
		Services:           serviceInfoProvider,
		ExtensionResolver:  types,
		DescriptorResolver: files,
	})
	return pbreflection.ResolveSchema(context.Background(), reflectionClient)
}

// MustBuildTool builds a tool definition from a ToolService method: the JSON
// schema is derived from the request message, the description from the
// method comment, and auto-execution from the idempotency level. Panics on
// failure — callers invoke it from init with compile-time-known methods, so
// failure is a build defect.
func MustBuildTool(toolName, handlerID string, methodFullName protoreflect.FullName) *aipb.Tool {
	schema, err := Schema()
	if err != nil {
		panic(fmt.Sprintf("loading tool schema: %v", err))
	}
	descriptor, err := schema.FindDescriptorByName(methodFullName)
	if err != nil {
		panic(fmt.Sprintf("finding method %s: %v", methodFullName, err))
	}
	methodDescriptor, ok := descriptor.(protoreflect.MethodDescriptor)
	if !ok {
		panic(fmt.Sprintf("%s is not a method", methodFullName))
	}
	jsonSchema, err := pbjson.NewSchemaBuilder(schema).BuildSchema(methodFullName)
	if err != nil {
		panic(fmt.Sprintf("building JSON schema for %s: %v", methodFullName, err))
	}
	annotations := map[string]string{ToolHandlerIDAnnotation: handlerID}
	// Side-effect-free methods are safe to auto-execute without user review;
	// the annotation propagates onto tool calls, where Review reads it back.
	if methodOptions, ok := methodDescriptor.Options().(*descriptorpb.MethodOptions); ok &&
		methodOptions.GetIdempotencyLevel() == descriptorpb.MethodOptions_NO_SIDE_EFFECTS {
		annotations[aitool.AnnotationKeyNoSideEffect] = "true"
	}
	return &aipb.Tool{
		Name:        toolName,
		Description: schema.GetComment(methodFullName, pbreflection.CommentStyleMultiline),
		JsonSchema:  jsonSchema,
		Annotations: annotations,
	}
}

// NoSideEffects reports whether a tool call targets a method declared
// side-effect free (idempotency_level = NO_SIDE_EFFECTS).
func NoSideEffects(toolCall *aipb.ToolCall) bool {
	// Locked read: the session goroutine mutates annotations concurrently.
	return GetToolCallAnnotation(toolCall, aitool.AnnotationKeyNoSideEffect) == "true"
}

// NewStructuredToolResult marshals a typed response proto into a structured
// tool result, so the model sees the documented response contract instead of
// hand-formatted strings.
func NewStructuredToolResult(toolCall *aipb.ToolCall, responseMessage proto.Message) (*aipb.ToolResult, error) {
	responseBytes, err := pbutil.JSONMarshal(responseMessage)
	if err != nil {
		return nil, fmt.Errorf("marshaling tool response: %w", err)
	}
	value := &structpb.Value{}
	if err := value.UnmarshalJSON(responseBytes); err != nil {
		return nil, fmt.Errorf("unmarshaling tool response into structpb.Value: %w", err)
	}
	return ai.NewStructuredToolResult(toolCall.Name, toolCall.Id, value), nil
}

// UnmarshalArguments parses a tool call's arguments into a typed request
// proto. Lenient: unknown/partial fields are tolerated (streaming).
func UnmarshalArguments(toolCall *aipb.ToolCall, requestMessage proto.Message) error {
	if err := pbutil.UnmarshalFromStruct(requestMessage, toolCall.GetArguments()); err != nil {
		return fmt.Errorf("parsing tool arguments: %w", err)
	}
	return nil
}
