package chat

import (
	"fmt"

	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	"github.com/malonaz/core/go/aip"
	"github.com/malonaz/core/go/pbutil/pbfieldmask"
	"github.com/spf13/cobra"

	chatscreen "github.com/malonaz/sgpt/cli/tui/screen/chat"
	chatservicepb "github.com/malonaz/sgpt/genproto/chat/chat_service/v1"
	chatpb "github.com/malonaz/sgpt/genproto/chat/v1"
	"github.com/malonaz/sgpt/internal/configuration"
)

func NewSummarizeCmd(config *configuration.Config, aiClient aiservicepb.AiServiceClient, chatClient chatservicepb.ChatServiceClient) *cobra.Command {
	return &cobra.Command{
		Use:   "summarize",
		Short: "Generate summaries for chats that have no title",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			listChatsRequest := &chatservicepb.ListChatsRequest{
				PageSize: 100,
				OrderBy:  "create_time desc",
				Filter:   "-metadata.title:*",
			}

			summarized := 0

			processChats := func(chats []*chatpb.Chat) (bool, error) {
				for _, chat := range chats {
					if chat.GetMetadata().GetTitle() != "" {
						return false, fmt.Errorf("chat %s already has a title", chat.GetName())
					}
					if len(chat.GetMetadata().GetMessages()) < 1 {
						return false, fmt.Errorf("chat %s has no messages", chat.GetName())
					}

					if err := chatscreen.GenerateChatSummary(ctx, config, aiClient, chatClient, chat); err != nil {
						return false, fmt.Errorf("generating summary for %s: %w", chat.GetName(), err)
					}

					updateChatRequest := &chatservicepb.UpdateChatRequest{
						Chat:       chat,
						UpdateMask: pbfieldmask.FromPaths("metadata.title").Proto(),
					}
					if _, err := chatClient.UpdateChat(ctx, updateChatRequest); err != nil {
						return false, fmt.Errorf("saving summary for %s: %w", chat.GetName(), err)
					}

					summarized++
				}
				return true, nil
			}

			if err := aip.PaginateFunc[*chatpb.Chat](ctx, listChatsRequest, chatClient.ListChats, processChats); err != nil {
				return fmt.Errorf("paginating chats: %w", err)
			}

			fmt.Printf("Summarized %d chats\n", summarized)
			return nil
		},
	}
}
