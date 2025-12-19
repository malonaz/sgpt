package chat

import (
	"fmt"
	"os"

	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	"github.com/spf13/cobra"

	"github.com/malonaz/sgpt/cli/chat/session"
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

			for i, chat := range listChatsResponse.Chats {
				fmt.Printf("Processing chat %d/%d (ID: %s)... ", i+1, len(listChatsResponse.Chats), chat.ID)

				if err := session.GenerateChatSummary(ctx, config, s, aiClient, chat); err != nil {
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
