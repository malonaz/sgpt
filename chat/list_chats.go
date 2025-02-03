package chat

import (
	"time"

	"github.com/sashabaranov/go-openai"
	"github.com/spf13/cobra"

	"github.com/malonaz/sgpt/internal/cli"
	"github.com/malonaz/sgpt/internal/configuration"
	"github.com/malonaz/sgpt/store"
)

// newListCmd instantiates and returns the chat list command.
func NewListChatsCmd(config *configuration.Config, s *store.Store) *cobra.Command {
	var opts struct {
		PageSize int
	}

	cmd := &cobra.Command{
		Use:   "list-chats",
		Short: "List all chats",
		Long:  "List all chats",
		Args:  cobra.ExactArgs(0),
		Run: func(cmd *cobra.Command, args []string) {
			// Headers.
			cli.Title("SGPT CHAT LIST")

			chats, err := s.List(opts.PageSize)
			cobra.CheckErr(err)
			for _, chat := range chats {
				cli.AIOutput("chat (%s) - %s\n", chat.ID, time.UnixMicro(chat.UpdateTimestamp).String())
				description := ""
				for i := 0; i < 10 && i < len(chat.Messages); i++ {
					if chat.Messages[i].Role == openai.ChatMessageRoleUser {
						description += "> " + chat.Messages[i].Content + "\n"
					}
				}
				cli.UserInput(description)
			}
		},
	}

	cmd.Flags().IntVarP(&opts.PageSize, "page-size", "p", 50, "Page size")
	return cmd
}
