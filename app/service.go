package app

import (
	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	chatservicepb "github.com/malonaz/sgpt/genproto/chat/chat_service/v1"
)

type App struct {
	AiClient   aiservicepb.AiClient
	ChatClient chatservicepb.ChatClient
}

func NewApp(aiClient aiservicepb.AiClient, chatClient chatservicepb.ChatClient) (*App, error) {
	return &App{
		AiClient:   aiClient,
		ChatClient: chatClient,
	}, nil
}
