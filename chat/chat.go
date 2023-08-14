package chat

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pkg/errors"
	"github.com/sashabaranov/go-openai"
	"github.com/shopspring/decimal"
	"github.com/spf13/cobra"

	"github.com/malonaz/sgpt/chat/store"
	"github.com/malonaz/sgpt/internal/cli"
	"github.com/malonaz/sgpt/internal/configuration"
	"github.com/malonaz/sgpt/internal/file"
	"github.com/malonaz/sgpt/internal/model"
	"github.com/malonaz/sgpt/internal/role"
)

const streamTokenTimeout = 5 * time.Second

// NewCmd instantiates and returns the chat command.
func NewCmd(openAIClient *openai.Client, config *configuration.Config) *cobra.Command {
	var opts struct {
		FileInjection *file.InjectionOpts
		Role          *role.Opts
		Model         *model.Opts
		ChatID        string
		Embeddings    bool
		ShowCost      bool
	}
	cmd := &cobra.Command{
		Use:   "chat",
		Short: "Back and forth chat",
		Long:  "Back and forth chat",
		Args:  cobra.ExactArgs(0),
		Run: func(cmd *cobra.Command, args []string) {
			// Instantiate store.
			s, err := store.New(config.ChatDirectory)
			cobra.CheckErr(err)

			// Parse a chat if relevant.opts.ChatID,
			var chat *store.Chat
			if opts.ChatID != "" {
				chat, err = s.Get(opts.ChatID)
				cobra.CheckErr(err)
			} else {
				opts.ChatID = uuid.New().String()[:8]
			}
			if chat == nil {
				chat = store.NewChat(opts.ChatID)
			}

			// Set the model.
			model, err := model.Parse(opts.Model, config)
			cobra.CheckErr(err)

			// Headers.
			cli.Title("SGPT CHAT [%s](%s)", model.ID, opts.ChatID)

			// Inject files.
			files, err := file.Parse(opts.FileInjection)
			cobra.CheckErr(err)
			additionalMessages := make([]openai.ChatCompletionMessage, 0, len(files))
			for _, file := range files {
				message := openai.ChatCompletionMessage{
					Role:    openai.ChatMessageRoleSystem,
					Content: fmt.Sprintf("file %s: `%s`", file.Path, file.Content),
				}
				additionalMessages = append(additionalMessages, message)
				cli.FileInfo("injecting file #%d: %s\n", len(additionalMessages), file.Path)
			}
			if len(additionalMessages) > 0 {
				tokens, cost, err := model.CalculateRequestCost(additionalMessages...)
				cobra.CheckErr(err)
				if opts.ShowCost {
					cli.CostInfo("File injections (%d tokens) will add %s per request\n", tokens, cost.String())
				}
			}

			// Inject role.
			r, err := role.Parse(opts.Role)
			cobra.CheckErr(err)
			if r != nil {
				message := openai.ChatCompletionMessage{
					Role:    openai.ChatMessageRoleSystem,
					Content: r.Description,
				}
				additionalMessages = append(additionalMessages, message)
			}

			// Print history.
			for _, message := range chat.Messages {
				if message.Role == openai.ChatMessageRoleUser {
					cli.UserInput("> %s\n", message.Content)
				}
				if message.Role == openai.ChatMessageRoleAssistant {
					cli.AIOutput(message.Content + "\n")
				}
			}

			ctx := context.Background()
			var totalCost decimal.Decimal
			for {
				// Query user for prompt.
				text, err := cli.PromptUser()
				cobra.CheckErr(err)
				// Quick feedback so user knows query has been submitted.
				cli.AIOutput("SGPT: ")

				// Set cancelable context with timeout.
				ctx, cancel := context.WithTimeout(ctx, time.Duration(config.RequestTimeout)*time.Second)
				defer cancel()

				// If relevant, fetch embeddings.
				var embeddingMessages []openai.ChatCompletionMessage
				if opts.Embeddings {
					embeddingMessages, err = getEmbeddingMessages(ctx, config, openAIClient, text)
					cobra.CheckErr(err)
				}

				// Create open AI request.
				userMessage := openai.ChatCompletionMessage{
					Role:    openai.ChatMessageRoleUser,
					Content: text,
				}
				messages := append(additionalMessages, embeddingMessages...)
				messages = append(messages, chat.Messages...)
				messages = append(messages, userMessage)
				request := openai.ChatCompletionRequest{
					Model:    model.ID,
					Messages: messages,
					Stream:   true,
				}
				requestTokens, requestCost, err := model.CalculateRequestCost(messages...)
				cobra.CheckErr(err)
				if opts.ShowCost {
					cli.CostInfo("Request contains %d tokens costing $%s\n", requestTokens, requestCost.String())
				}

				// Initiate Open AI stream.
				stream, err := openAIClient.CreateChatCompletionStream(ctx, request)
				cobra.CheckErr(err)
				defer stream.Close()
				tokenChannel, errorChannel := pipeStream(stream)

				// Process Open AI stream.
				interruptSignalChannel := make(chan os.Signal, 1)
				signal.Notify(interruptSignalChannel, os.Interrupt)
				interrupted := false
				chatCompletionMessage := openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant}
				for {
					streamEnded := false
					select {
					case <-interruptSignalChannel:
						// We've detected an interrupt, kill the stream.
						cli.UserCommand("#Interrupted")
						stream.Close()
						interrupted = true
						streamEnded = true
					case token := <-tokenChannel:
						content := strings.ReplaceAll(token, "%", "%%")
						cli.AIOutput(content)
						chatCompletionMessage.Content += content
					case err := <-errorChannel:
						if errors.Is(err, io.EOF) {
							streamEnded = true
						}
					}
					if streamEnded {
						// Stop listening to signal.
						signal.Stop(interruptSignalChannel)
						break
					}
				}
				cli.AIOutput("\n")

				responseTokens, responseCost, err := model.CalculateResponseCost(chatCompletionMessage)
				cobra.CheckErr(err)
				totalCost = totalCost.Add(requestCost).Add(responseCost)
				if opts.ShowCost {
					cli.CostInfo("Response contains %d tokens costing $%s\n", responseTokens, responseCost.String())
					cli.CostInfo("Total cost so far $%s\n", totalCost.String())
				}

				if !interrupted {
					// Append the response content to our history.
					chat.Messages = append(chat.Messages, userMessage, chatCompletionMessage)
					// Save chat.
					err := s.Write(chat)
					cobra.CheckErr(err)
				}
			}
		},
	}

	opts.FileInjection = file.GetOpts(cmd)
	opts.Role = role.GetOpts(cmd)
	opts.Model = model.GetOpts(cmd, config)
	cmd.Flags().StringVar(&opts.ChatID, "id", "", "specify a chat id. Defaults to latest one")
	cmd.Flags().BoolVarP(&opts.Embeddings, "embeddings", "e", false, "Use embeddings")
	cmd.Flags().BoolVarP(&opts.ShowCost, "show-cost", "c", false, "Show cost")

	cmd.AddCommand(newListCmd(config))
	return cmd
}
