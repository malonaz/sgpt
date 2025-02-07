package chat

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/malonaz/sgpt/internal/configuration"
	"github.com/malonaz/sgpt/store"
)

// NewGenerateChatTitlesCmd instantiates and returns the generate-chat-titles command.
func NewGenerateChatTitlesCmd(config *configuration.Config, s *store.Store) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "generate-chat-titles",
		Short: "Generate titles for chats that don't have one",
		Long:  "Generate titles for all chats in the database that don't have a title",
		Run: func(cmd *cobra.Command, args []string) {
			// Create context with timeout
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()

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

				if err := generateChatSummary(ctx, config, s, chat); err != nil {
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
