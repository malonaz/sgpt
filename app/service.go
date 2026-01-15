package app

import (
	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	chatservicepb "github.com/malonaz/sgpt/genproto/chat/chat_service/v1"
)

type App struct {
	AiServiceClient   aiservicepb.AiServiceClient
	ChatServiceClient chatservicepb.ChatServiceClient
}

func NewApp(aiServiceClient aiservicepb.AiServiceClient, chatServiceClient chatservicepb.ChatServiceClient) (*App, error) {
	return &App{
		AiServiceClient:   aiServiceClient,
		ChatServiceClient: chatServiceClient,
	}, nil
}
