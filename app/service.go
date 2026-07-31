package app

import (
	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
)

type App struct {
	AiServiceClient aiservicepb.AiServiceClient
}

func NewApp(aiServiceClient aiservicepb.AiServiceClient) (*App, error) {
	return &App{
		AiServiceClient: aiServiceClient,
	}, nil
}
