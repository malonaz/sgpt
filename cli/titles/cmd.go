package titles

import (
	"fmt"
	"strings"

	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"
	"github.com/spf13/cobra"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/store"
)

// NewCmd backfills titles on untitled chats, reusing the session's titling
// logic: only user-authored text (context messages excluded) is fed to the
// configured summary model.
func NewCmd(config *sgptpb.Configuration, aiClient aiservicepb.AiServiceClient) *cobra.Command {
	chatStore := store.New(config, aiClient)

	var dryRun bool
	cmd := &cobra.Command{
		Use:   "titles",
		Short: "Generate titles for untitled chats that have at least one user message",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			pageToken := ""
			for {
				chats, nextPageToken, err := chatStore.ListChats(ctx, 100, pageToken, "")
				if err != nil {
					return err
				}
				for _, chat := range chats {
					if chat.GetTitle() != "" {
						continue
					}
					if err := titleChat(cmd, chatStore, chat, dryRun); err != nil {
						// Best-effort backfill: report and keep going.
						fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", chat.GetName(), err)
					}
				}
				if nextPageToken == "" {
					return nil
				}
				pageToken = nextPageToken
			}
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print generated titles without persisting them")
	return cmd
}

// titleChat mirrors session.maybeGenerateTitle: extract user-authored text,
// cap it, generate, persist with a title-only mask.
func titleChat(cmd *cobra.Command, chatStore *store.Store, chat *aipb.Chat, dryRun bool) error {
	ctx := cmd.Context()
	messages, err := chatStore.ListMessages(ctx, chat.GetName())
	if err != nil {
		return err
	}
	var parts []string
	for _, message := range messages {
		if message.GetRole() != aipb.Role_ROLE_USER || store.IsContextMessage(message) {
			continue
		}
		for _, block := range ai.FilterBlocks(message.GetBlocks(), ai.BlockTypeText) {
			parts = append(parts, block.GetText())
		}
	}
	userText := strings.TrimSpace(strings.Join(parts, "\n"))
	if userText == "" {
		// No user-authored text: nothing to title from.
		return nil
	}
	// Cap the excerpt: the summary model only needs the gist.
	const maxTitleInputLength = 2000
	if len(userText) > maxTitleInputLength {
		userText = userText[:maxTitleInputLength]
	}
	title, err := chatStore.GenerateTitle(ctx, userText)
	if err != nil {
		return err
	}
	if title == "" {
		return fmt.Errorf("no summary model configured")
	}
	if dryRun {
		fmt.Fprintf(cmd.OutOrStdout(), "%s: %s (dry-run)\n", chat.GetName(), title)
		return nil
	}
	chat.Title = title
	if _, err := chatStore.UpdateChat(ctx, chat, "title"); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", chat.GetName(), title)
	return nil
}
