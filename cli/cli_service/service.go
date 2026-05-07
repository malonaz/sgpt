package cli_service

import (
	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/grpc"

	sgptservicepb "github.com/malonaz/sgpt/genproto/sgpt/sgpt_service/v1"
	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/toolengine"
)

// Service holds shared clients and configuration for the CLI app layer.
type Service struct {
	Config                  *sgptpb.Configuration
	AIClient                aiservicepb.AiServiceClient
	ChatClient              sgptservicepb.SgptServiceClient
	BaseURLToGRPCConnection map[string]*grpc.Connection
}

// SessionParams bundles per-chat parameters passed when creating a session.
type SessionParams struct {
	Model              *aipb.Model
	Role               *sgptpb.Role
	MaxTokens          int32
	Temperature        float64
	ReasoningEffort    aipb.ReasoningEffort
	EnableTools        bool
	Chat               string
	ToolEngineManager  *toolengine.Manager
	AdditionalMessages []*aipb.Message
	InjectedFiles      []string
}
