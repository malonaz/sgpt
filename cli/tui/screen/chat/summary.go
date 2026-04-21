package chat

import (
	"context"
	"fmt"
	"strings"

	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"

	sgptservicepb "github.com/malonaz/sgpt/genproto/sgpt/sgpt_service/v1"
	chatpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/configuration"
)

func GenerateChatSummary(ctx context.Context, config *configuration.Config, aiClient aiservicepb.AiServiceClient, chatClient sgptservicepb.SgptServiceClient, chat *chatpb.Chat) error {
	if config.Chat.SummaryModel == "" {
		return nil
	}
	if len(chat.Metadata.Messages) < 1 {
		return fmt.Errorf("expected at least 1 message, found %d", len(chat.Metadata.Messages))
	}

	userMessage := chat.Metadata.Messages[0].Message
	if userMessage.Role != aipb.Role_ROLE_USER {
		return fmt.Errorf("expected first message to be user role")
	}

	summaryModel, err := config.ResolveModelAlias(config.Chat.SummaryModel)
	if err != nil {
		return fmt.Errorf("resolving summary model alias: %w", err)
	}

	systemPrompt := `Generate a brief, concise title (max 6 words) for this conversation so far. YOU MUST ALWAYS OUTPUT SOMETHING.`
	messages := []*aipb.Message{
		ai.NewSystemMessage(ai.NewTextBlock(systemPrompt)),
		userMessage,
	}

	textToTextRequest := &aiservicepb.TextToTextRequest{
		Model:    summaryModel,
		Messages: messages,
		Configuration: &aiservicepb.TextToTextConfiguration{
			MaxTokens: 50,
		},
	}

	textToTextResponse, err := aiClient.TextToText(ctx, textToTextRequest)
	if err != nil {
		return fmt.Errorf("generating summary: %w", err)
	}

	var summary string
	for _, block := range ai.FilterBlocks(textToTextResponse.GetMessage().GetBlocks(), ai.BlockTypeText) {
		summary += block.GetText()
	}
	cleanSummary := strings.TrimSpace(summary)
	cleanSummary = strings.Trim(cleanSummary, `"'`)
	cleanSummary = strings.ReplaceAll(cleanSummary, "\n", " ")

	chat.Metadata.Title = cleanSummary
	return nil
}
