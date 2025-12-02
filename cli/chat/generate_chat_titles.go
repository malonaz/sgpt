package chat

import (
	"context"
	"fmt"
	"os"
	"strings"

	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/spf13/cobra"

	"github.com/malonaz/sgpt/internal/configuration"
	"github.com/malonaz/sgpt/store"
)

// NewGenerateChatTitlesCmd instantiates and returns the generate-chat-titles command.
func NewGenerateChatTitlesCmd(config *configuration.Config, s *store.Store, aiClient aiservicepb.AiClient) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "generate-chat-titles",
		Short: "Generate titles for chats that don't have one",
		Long:  "Generate titles for all chats in the database that don't have a title",
		Run: func(cmd *cobra.Command, args []string) {
			ctx := cmd.Context()

			// Fetch all chats without titles
			listChatsRequest := &store.ListChatsRequest{
				Filter: "title IS NULL",
			}
			listChatsResponse, err := s.ListChats(listChatsRequest)
			if err != nil {
				fmt.Printf("Error fetching chats without titles: %v\n", err)
				os.Exit(1)
			}

			if len(listChatsResponse.Chats) == 0 {
				fmt.Println("No chats found without titles")
				return
			}

			fmt.Printf("Found %d chats without titles\n", len(listChatsResponse.Chats))

			// Generate titles for each chat
			for i, chat := range listChatsResponse.Chats {
				fmt.Printf("Processing chat %d/%d (ID: %s)... ", i+1, len(listChatsResponse.Chats), chat.ID)

				if err := generateChatSummary(ctx, config, s, aiClient, chat); err != nil {
					fmt.Printf("ERROR: %v\n", err)
					continue
				}
				fmt.Printf("Done\n")
			}

			fmt.Println("Finished processing all chats")
		},
	}

	return cmd
}

// generateChatSummary creates a title/summary for the chat using the specified model
func generateChatSummary(ctx context.Context, config *configuration.Config, s *store.Store, aiClient aiservicepb.AiClient, chat *store.Chat) error {
	if config.Chat.SummaryModel == "" {
		return nil
	}
	if len(chat.Messages) < 2 {
		return fmt.Errorf("expected at least 2 messages, found %d", len(chat.Messages))
	}
	if chat.Messages[0].Role != aipb.Role_ROLE_USER || chat.Messages[1].Role != aipb.Role_ROLE_ASSISTANT {
		return fmt.Errorf("expected first message to be user role and second message to be assistant role")
	}

	// Resolve summary model alias
	summaryModel, err := config.ResolveModelAlias(config.Chat.SummaryModel)
	if err != nil {
		return fmt.Errorf("resolving summary model alias: %w", err)
	}

	// Create prompt for summary generation
	summaryPrompt := &aipb.Message{
		Role:    aipb.Role_ROLE_USER,
		Content: "Generate a brief, concise title (max 6 words) for this conversation so far. YOU MUST ALWAYS OUTPUT SOMETHING.",
	}
	messages := append([]*aipb.Message{}, chat.Messages[:2]...)
	messages = append(messages, summaryPrompt)

	// Create request
	request := &aiservicepb.TextToTextRequest{
		Model:    summaryModel,
		Messages: messages,
		Configuration: &aiservicepb.TextToTextConfiguration{
			MaxTokens: 50, // Should be enough for a short title
		},
	}

	// Get response
	response, err := aiClient.TextToText(ctx, request)
	if err != nil {
		return fmt.Errorf("failed to generate summary: %w", err)
	}

	// Clean up the summary
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
