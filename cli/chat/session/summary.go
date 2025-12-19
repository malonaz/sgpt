package session

import (
	"context"
	"fmt"
	"strings"

	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	aipb "github.com/malonaz/core/genproto/ai/v1"

	"github.com/malonaz/sgpt/internal/configuration"
	"github.com/malonaz/sgpt/store"
)

// GenerateChatSummary creates a title/summary for the chat using the specified model.
func GenerateChatSummary(ctx context.Context, config *configuration.Config, s *store.Store, aiClient aiservicepb.AiClient, chat *store.Chat) error {
	if config.Chat.SummaryModel == "" {
		return nil
	}
	if len(chat.Messages) < 2 {
		return fmt.Errorf("expected at least 2 messages, found %d", len(chat.Messages))
	}
	if chat.Messages[0].Role != aipb.Role_ROLE_USER || chat.Messages[1].Role != aipb.Role_ROLE_ASSISTANT {
		return fmt.Errorf("expected first message to be user role and second message to be assistant role")
	}

	summaryModel, err := config.ResolveModelAlias(config.Chat.SummaryModel)
	if err != nil {
		return fmt.Errorf("resolving summary model alias: %w", err)
	}

	summaryPrompt := &aipb.Message{
		Role:    aipb.Role_ROLE_USER,
		Content: "Generate a brief, concise title (max 6 words) for this conversation so far. YOU MUST ALWAYS OUTPUT SOMETHING.",
	}
	messages := append([]*aipb.Message{}, chat.Messages[:2]...)
	messages = append(messages, summaryPrompt)

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

	cleanSummary := strings.TrimSpace(response.Message.Content)
	cleanSummary = strings.Trim(cleanSummary, `"'`)
	cleanSummary = strings.ReplaceAll(cleanSummary, "\n", " ")

	if cleanSummary != "" {
		chat.Title = &cleanSummary
		updateChatRequest := &store.UpdateChatRequest{
			Chat:       chat,
			UpdateMask: []string{"title"},
		}
		if err := s.UpdateChat(updateChatRequest); err != nil {
			return fmt.Errorf("updating chat title: %w", err)
		}
	}

	return nil
}
