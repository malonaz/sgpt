package app

import (
	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	sgptservicepb "github.com/malonaz/sgpt/genproto/sgpt/sgpt_service/v1"
)

type App struct {
	AiServiceClient   aiservicepb.AiServiceClient
	SgptServiceClient sgptservicepb.SgptServiceClient
}

func NewApp(aiServiceClient aiservicepb.AiServiceClient, sgptServiceClient sgptservicepb.SgptServiceClient) (*App, error) {
	return &App{
		AiServiceClient:   aiServiceClient,
		SgptServiceClient: sgptServiceClient,
	}, nil
}
