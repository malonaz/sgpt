package chat

import (
	"fmt"

	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	"github.com/malonaz/core/go/aip"
	"github.com/malonaz/core/go/pbutil/pbfieldmask"
	"github.com/spf13/cobra"

	chatscreen "github.com/malonaz/sgpt/cli/tui/screen/chat"
	sgptservicepb "github.com/malonaz/sgpt/genproto/sgpt/sgpt_service/v1"
	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
)

func NewSummarizeCmd(config *sgptpb.Configuration, aiClient aiservicepb.AiServiceClient, chatClient sgptservicepb.SgptServiceClient) *cobra.Command {
	return &cobra.Command{
		Use:   "summarize",
		Short: "Generate summaries for chats that have no title",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			listChatsRequest := &sgptservicepb.ListChatsRequest{
				PageSize: 100,
				OrderBy:  "create_time desc",
				Filter:   "-metadata.title:*",
			}

			var summarized int
			for chat, err := range aip.Iterator[*sgptpb.Chat](ctx, listChatsRequest, chatClient.ListChats) {
				if err != nil {
					return fmt.Errorf("paginating chats: %w", err)
				}
				if chat.GetMetadata().GetTitle() != "" {
					return fmt.Errorf("chat %s already has a title", chat.GetName())
				}
				if len(chat.GetMetadata().GetMessages()) < 1 {
					return fmt.Errorf("chat %s has no messages", chat.GetName())
				}

				if err := chatscreen.GenerateChatSummary(ctx, config, aiClient, chatClient, chat); err != nil {
					return fmt.Errorf("generating summary for %s: %w", chat.GetName(), err)
				}

				updateChatRequest := &sgptservicepb.UpdateChatRequest{
					Chat:       chat,
					UpdateMask: pbfieldmask.FromPaths("metadata.title").Proto(),
				}
				if _, err := chatClient.UpdateChat(ctx, updateChatRequest); err != nil {
					return fmt.Errorf("saving summary for %s: %w", chat.GetName(), err)
				}

				summarized++
			}

			fmt.Printf("Summarized %d chats\n", summarized)
			return nil
		},
	}
}
