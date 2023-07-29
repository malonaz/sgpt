package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pkg/errors"
	"github.com/sashabaranov/go-openai"
	"github.com/shopspring/decimal"
	"github.com/spf13/cobra"

	"github.com/malonaz/sgpt/cli"
	"github.com/malonaz/sgpt/configuration"
	"github.com/malonaz/sgpt/embed"
	"github.com/malonaz/sgpt/file"
	"github.com/malonaz/sgpt/model"
	"github.com/malonaz/sgpt/role"
)

// NewCmd instantiates and returns the inventory chat cmd.
func NewCmd(openAIClient *openai.Client, config *configuration.Config) *cobra.Command {
	// Initialize chat directory.
	err := initializeChatDirectoryIfNotExist(config.ChatDirectory)
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
					cli.UserInput(message.Content)
				}
				if message.Role == openai.ChatMessageRoleAssistant {
					cli.AIInput(message.Content)
				}
			}

			ctx := context.Background()
			var totalCost decimal.Decimal
			for {
				// Query user for prompt.
				cli.UserInput("")
				text, err := cli.PromptUser()
				cobra.CheckErr(err)
				// convert CRLF to LF
				text = strings.ReplaceAll(text, "\n", " ")
				var embeddingMessages []openai.ChatCompletionMessage
				if opts.Embeddings {
					store, err := embed.LoadStore()
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
				chat.Messages = append(chat.Messages, openai.ChatCompletionMessage{
					Role:    openai.ChatMessageRoleUser,
					Content: text,
				})

				// Send request to API and stream response.
				ctx, cancel := context.WithTimeout(ctx, time.Duration(config.RequestTimeout)*time.Second)
				defer cancel()
				messages := append(additionalMessages, embeddingMessages...)
				messages = append(messages, chat.Messages...)
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

				stream, err := openAIClient.CreateChatCompletionStream(ctx, request)
				cobra.CheckErr(err)
				defer stream.Close()

				chatCompletionMessage := openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant}
				for {
					response, err := stream.Recv()
					if errors.Is(err, io.EOF) {
						cli.AIInput("\n")
						break
					}
					cobra.CheckErr(err)
					content := strings.Replace(response.Choices[0].Delta.Content, "\n\n", "\n", -1)
					content = strings.Replace(content, "%", "%%", -1)
					cli.AIInput(content)
					chatCompletionMessage.Content += response.Choices[0].Delta.Content
				}
				responseTokens, responseCost, err := model.CalculateResponseCost(chatCompletionMessage)
				cobra.CheckErr(err)
				totalCost = totalCost.Add(requestCost).Add(responseCost)
				if opts.ShowCost {
					cli.CostInfo("Response contains %d tokens costing $%s\n", responseTokens, responseCost.String())
					cli.CostInfo("Total cost so far $%s\n", totalCost.String())
				}

				// Append the response content to our history.
				chat.Messages = append(chat.Messages, chatCompletionMessage)

				// Save chat.
				err = chat.Save(config.ChatDirectory, opts.ChatID)
				cobra.CheckErr(err)
			}
		},
	}

	opts.FileInjection = file.GetOpts(cmd)
	opts.Role = role.GetOpts(cmd)
	opts.Model = model.GetOpts(cmd, config)
	cmd.Flags().StringVar(&opts.ChatID, "id", "", "specify a chat id. Defaults to latest one")
	cmd.Flags().BoolVarP(&opts.Embeddings, "embeddings", "e", false, "Use embeddings")
	cmd.Flags().BoolVar(&opts.ShowCost, "show-cost", false, "Show cost")
	return cmd
}

func initializeChatDirectoryIfNotExist(chatDirectory string) error {
	if _, err := os.Stat(chatDirectory); !os.IsNotExist(err) {
		return nil
	}
	if err := os.MkdirAll(chatDirectory, 0755); err != nil {
		return errors.Wrap(err, "creating chat directory")
	}
	return nil
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
