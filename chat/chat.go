package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pkg/errors"
	"github.com/sashabaranov/go-openai"
	"github.com/shopspring/decimal"
	"github.com/spf13/cobra"

	"github.com/malonaz/sgpt/embed"
	"github.com/malonaz/sgpt/internal/cli"
	"github.com/malonaz/sgpt/internal/configuration"
	"github.com/malonaz/sgpt/internal/file"
	"github.com/malonaz/sgpt/internal/model"
	"github.com/malonaz/sgpt/internal/role"
)

// NewCmd instantiates and returns the inventory chat cmd.
func NewCmd(openAIClient *openai.Client, config *configuration.Config) *cobra.Command {
	// Initialize chat directory.
	err := file.CreateDirectoryIfNotExist(config.ChatDirectory)
	cobra.CheckErr(err)

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
			// Parse a chat if relevant.opts.ChatID,
			chat := &Chat{}
			if opts.ChatID != "" {
				chat, err = parseChat(config.ChatDirectory, opts.ChatID)
				cobra.CheckErr(err)
			} else {
				opts.ChatID = uuid.New().String()[:8]
			}

			// Set the model.
			model, err := model.Parse(opts.Model, config)
			cobra.CheckErr(err)

			// Headers.
			cli.Separator()
			cli.Title("SGPT CHAT [%s](%s)", model.ID, opts.ChatID)
			cli.Separator()

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
				var embeddingMessages []openai.ChatCompletionMessage
				if opts.Embeddings {
					store, err := embed.LoadStore(config)
					cobra.CheckErr(err)
					embeddings, err := embed.Content(ctx, openAIClient, text)
					cobra.CheckErr(err)
					chunks, err := store.Search(embeddings)
					cobra.CheckErr(err)
					if len(chunks) != 0 {
						embeddingMessages = append(embeddingMessages, openai.ChatCompletionMessage{
							Role:    openai.ChatMessageRoleSystem,
							Content: role.EmbeddingsAugmentedAssistant,
						})
						for i := 0; i < 10; i++ {
							chunk := chunks[i]
							cli.FileInfo("inserting chunk from file %s\n", chunk.Filename)
							embeddingMessages = append(embeddingMessages, openai.ChatCompletionMessage{
								Role:    openai.ChatMessageRoleSystem,
								Content: chunk.Content,
							})
						}
					}
				}
				cli.AIOutput("SGPT: ")
				userMessage := openai.ChatCompletionMessage{
					Role:    openai.ChatMessageRoleUser,
					Content: text,
				}

				// Create open AI request.
				ctx, cancel := context.WithTimeout(ctx, time.Duration(config.RequestTimeout)*time.Second)
				defer cancel()
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

				// We will use channels to detect ctrl+c events.
				interrupSignalChannel := make(chan os.Signal, 1)
				signal.Notify(interrupSignalChannel, os.Interrupt)
				interrupted := false
				chatCompletionMessage := openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant}
				for {
					select {
					case <-interrupSignalChannel:
						// We've detected an interrupt, kill the stream.
						cli.UserCommand("#Interrupted\n")
						stream.Close()
						interrupted = true
					default:
					}
					if interrupted {
						break
					}

					response, err := stream.Recv()
					if errors.Is(err, io.EOF) {
						cli.AIOutput("\n")
						break
					}
					cobra.CheckErr(err)
					content := response.Choices[0].Delta.Content
					content = strings.Replace(content, "%", "%%", -1)
					cli.AIOutput(content)
					chatCompletionMessage.Content += response.Choices[0].Delta.Content
				}
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
					err = chat.Save(config.ChatDirectory, opts.ChatID)
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
	return cmd
}

// Chat holds a chat struct.
type Chat struct {
	Messages []openai.ChatCompletionMessage
}

func parseChat(chatDirectory, chatID string) (*Chat, error) {
	path := path.Join(chatDirectory, chatID)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return &Chat{}, nil
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.Wrap(err, "reading chat file")
	}
	chat := &Chat{}
	if err = json.Unmarshal(bytes, chat); err != nil {
		return nil, errors.Wrap(err, "unmarshaling into chat")
	}
	return chat, nil
}

// Save a chat to disk.
func (c *Chat) Save(chatDirectory, chatID string) error {
	path := path.Join(chatDirectory, chatID)
	bytes, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return errors.Wrap(err, "marshaling chat to JSON")
	}

	if err := os.WriteFile(path, bytes, 0644); err != nil {
		return errors.Wrap(err, "writing chat to file")
	}
	return nil
}
