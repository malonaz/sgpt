package tool

import (
	"fmt"
	"sync"

	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/pbutil"
	"google.golang.org/protobuf/proto"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
)

const (
	ToolCallMetadataAnnotationKey   = "sgpt.com/tool-call-metadata"
	ToolResultMetadataAnnotationKey = "sgpt.com/tool-result-metadata"
)

// annotationsMu guards tool call/result annotation maps: the session
// goroutine mutates them (status, metadata) while the TUI goroutine reads
// them during renders — unsynchronized map access is a fatal runtime error.
var annotationsMu sync.RWMutex

// SnapshotToolCall returns a deep copy taken under the annotations lock, for
// code paths (e.g. core's ParseToolCall) that walk the maps directly and
// would otherwise race with concurrent annotation writes.
func SnapshotToolCall(toolCall *aipb.ToolCall) *aipb.ToolCall {
	annotationsMu.RLock()
	defer annotationsMu.RUnlock()
	return proto.Clone(toolCall).(*aipb.ToolCall)
}

// GetToolCallAnnotation reads an arbitrary tool call annotation under the
// annotations lock. Any read of the annotations map outside this package's
// accessors races with the session goroutine's status/metadata writes.
func GetToolCallAnnotation(toolCall *aipb.ToolCall, key string) string {
	annotationsMu.RLock()
	defer annotationsMu.RUnlock()
	return toolCall.GetAnnotations()[key]
}

func SetToolCallMetadata(toolCall *aipb.ToolCall, metadata *sgptpb.ToolCallMetadata) error {
	bytes, err := pbutil.JSONMarshal(metadata)
	if err != nil {
		return fmt.Errorf("marshaling tool call metadata: %w", err)
	}
	annotationsMu.Lock()
	defer annotationsMu.Unlock()
	if toolCall.Annotations == nil {
		toolCall.Annotations = map[string]string{}
	}
	toolCall.Annotations[ToolCallMetadataAnnotationKey] = string(bytes)
	return nil
}

func ParseToolCallMetadata(toolCall *aipb.ToolCall) (*sgptpb.ToolCallMetadata, error) {
	annotationsMu.RLock()
	raw, ok := toolCall.Annotations[ToolCallMetadataAnnotationKey]
	annotationsMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("tool call missing %s annotation", ToolCallMetadataAnnotationKey)
	}
	metadata := &sgptpb.ToolCallMetadata{}
	if err := pbutil.JSONUnmarshal([]byte(raw), metadata); err != nil {
		return nil, fmt.Errorf("unmarshaling tool call metadata: %w", err)
	}
	return metadata, nil
}

func SetToolResultMetadata(toolResult *aipb.ToolResult, metadata *sgptpb.ToolCallResultMetadata) error {
	bytes, err := pbutil.JSONMarshal(metadata)
	if err != nil {
		return fmt.Errorf("marshaling tool result metadata: %w", err)
	}
	annotationsMu.Lock()
	defer annotationsMu.Unlock()
	if toolResult.Annotations == nil {
		toolResult.Annotations = map[string]string{}
	}
	toolResult.Annotations[ToolResultMetadataAnnotationKey] = string(bytes)
	return nil
}

func ParseToolResultMetadata(toolResult *aipb.ToolResult) (*sgptpb.ToolCallResultMetadata, error) {
	annotationsMu.RLock()
	raw, ok := toolResult.Annotations[ToolResultMetadataAnnotationKey]
	annotationsMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("tool result missing %s annotation", ToolResultMetadataAnnotationKey)
	}
	metadata := &sgptpb.ToolCallResultMetadata{}
	if err := pbutil.JSONUnmarshal([]byte(raw), metadata); err != nil {
		return nil, fmt.Errorf("unmarshaling tool result metadata: %w", err)
	}
	return metadata, nil
}
