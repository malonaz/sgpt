package tools

import (
	"fmt"

	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/pbutil"
	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
)

const (
	ToolCallMetadataAnnotationKey   = "sgpt.com/tool-call-metadata"
	ToolResultMetadataAnnotationKey = "sgpt.com/tool-result-metadata"

	ToolCallStatusAnnotation = "sgpt.com/tool-status"
	ToolCallStatusPending    = "pending"
	ToolCallStatusAccepted   = "accepted"
	ToolCallStatusRejected   = "rejected"

	ToolCallRejectionReasonAnnotation = "sgpt.com/tool-rejection-reason"
)

func SetToolCallStatus(toolCall *aipb.ToolCall, status string) {
	if toolCall.Annotations == nil {
		toolCall.Annotations = map[string]string{}
	}
	toolCall.Annotations[ToolCallStatusAnnotation] = status
}

func GetToolCallStatus(toolCall *aipb.ToolCall) string {
	return toolCall.GetAnnotations()[ToolCallStatusAnnotation]
}

func SetToolCallRejectionReason(toolCall *aipb.ToolCall, reason string) {
	if toolCall.Annotations == nil {
		toolCall.Annotations = map[string]string{}
	}
	toolCall.Annotations[ToolCallRejectionReasonAnnotation] = reason
}

func GetToolCallRejectionReason(toolCall *aipb.ToolCall) string {
	return toolCall.GetAnnotations()[ToolCallRejectionReasonAnnotation]
}

func SetToolCallMetadata(toolCall *aipb.ToolCall, metadata *sgptpb.ToolCallMetadata) error {
	bytes, err := pbutil.JSONMarshal(metadata)
	if err != nil {
		return fmt.Errorf("marshaling tool call metadata: %w", err)
	}
	if toolCall.Annotations == nil {
		toolCall.Annotations = map[string]string{}
	}
	toolCall.Annotations[ToolCallMetadataAnnotationKey] = string(bytes)
	return nil
}

func ParseToolCallMetadata(toolCall *aipb.ToolCall) (*sgptpb.ToolCallMetadata, error) {
	raw, ok := toolCall.Annotations[ToolCallMetadataAnnotationKey]
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
	if toolResult.Annotations == nil {
		toolResult.Annotations = map[string]string{}
	}
	toolResult.Annotations[ToolResultMetadataAnnotationKey] = string(bytes)
	return nil
}

func ParseToolResultMetadata(toolResult *aipb.ToolResult) (*sgptpb.ToolCallResultMetadata, error) {
	raw, ok := toolResult.Annotations[ToolResultMetadataAnnotationKey]
	if !ok {
		return nil, fmt.Errorf("tool result missing %s annotation", ToolResultMetadataAnnotationKey)
	}
	metadata := &sgptpb.ToolCallResultMetadata{}
	if err := pbutil.JSONUnmarshal([]byte(raw), metadata); err != nil {
		return nil, fmt.Errorf("unmarshaling tool result metadata: %w", err)
	}
	return metadata, nil
}
