package tui

import (
	"context"
	"fmt"
	"strings"

	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"
	"github.com/malonaz/core/go/pbutil/pbfieldmask"
	chatservicepb "github.com/malonaz/sgpt/genproto/chat/chat_service/v1"
	chatpb "github.com/malonaz/sgpt/genproto/chat/v1"

	"github.com/malonaz/sgpt/internal/configuration"
)

// GenerateChatSummary creates a title/summary for the chat using the specified model.
func GenerateChatSummary(ctx context.Context, config *configuration.Config, aiClient aiservicepb.AiServiceClient, chatClient chatservicepb.ChatServiceClient, chat *chatpb.Chat) error {
	if config.Chat.SummaryModel == "" {
		return nil
	}
	if len(chat.Metadata.Messages) < 1 {
		return fmt.Errorf("expected at least 2 messages, found %d", len(chat.Metadata.Messages))
	}
	userMessage := chat.Metadata.Messages[0].Message
	if userMessage.GetUser() == nil {
		return fmt.Errorf("expected first message to be user role")
	}

	summaryModel, err := config.ResolveModelAlias(config.Chat.SummaryModel)
	if err != nil {
		return fmt.Errorf("resolving summary model alias: %w", err)
	}

	systemPrompt := `Generate a brief, concise title (max 6 words) for this conversation so far. YOU MUST ALWAYS OUTPUT SOMETHING.`
	messages := []*aipb.Message{
		ai.NewSystemMessage(&aipb.SystemMessage{Content: systemPrompt}),
		userMessage,
	}

	request := &aiservicepb.TextToTextRequest{
		Model:    summaryModel,
		Messages: messages,
		Configuration: &aiservicepb.TextToTextConfiguration{
			MaxTokens: 50,
		},
	}

	response, err := aiClient.TextToText(ctx, request)
	if err != nil {
		return fmt.Errorf("failed to generate summary: %w", err)
	}

	cleanSummary := strings.TrimSpace(response.Message.GetAssistant().GetContent())
	cleanSummary = strings.Trim(cleanSummary, `"'`)
	cleanSummary = strings.ReplaceAll(cleanSummary, "\n", " ")

	if cleanSummary != "" {
		chat.Metadata.Title = cleanSummary
		updateChatRequest := &chatservicepb.UpdateChatRequest{
			Chat:       chat,
			UpdateMask: pbfieldmask.FromPaths("metadata.title").Proto(),
		}
		if _, err := chatClient.UpdateChat(ctx, updateChatRequest); err != nil {
			return fmt.Errorf("updating chat title: %w", err)
		}
	}

	return nil
}
