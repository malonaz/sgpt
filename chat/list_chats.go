package chat

import (
	"time"

	"github.com/sashabaranov/go-openai"
	"github.com/spf13/cobra"

	"github.com/malonaz/sgpt/chat/store"
	"github.com/malonaz/sgpt/internal/cli"
	"github.com/malonaz/sgpt/internal/configuration"
)

// newListCmd instantiates and returns the chat list command.
func newListCmd(config *configuration.Config) *cobra.Command {
	var opts struct {
		PageSize int
	}

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all chats",
		Long:  "List all chats",
		Args:  cobra.ExactArgs(0),
		Run: func(cmd *cobra.Command, args []string) {
			// Instantiate store.
			s, err := store.New(config.Chat.Directory)
			cobra.CheckErr(err)

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
