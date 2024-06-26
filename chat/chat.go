package chat

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"regexp"
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
const doNotSendToken = "%@#$!@"

var imagePromptRegexp = regexp.MustCompile(`prompt\((.*?)\)`)

// NewCmd instantiates and returns the chat command.
func NewCmd(openAIClient *openai.Client, config *configuration.Config) *cobra.Command {
	var opts struct {
		FileInjection *file.InjectionOpts
		Role          *role.Opts
		Model         *model.Opts
		ChatID        string
		Embeddings    bool
		ShowCost      bool
		Continue      bool
		ImageSize     string
		ImageQuality  string
		ImageNumber   int
	}
	cmd := &cobra.Command{
		Use:   "chat",
		Short: "Back and forth chat",
		Long:  "Back and forth chat",
		Args:  cobra.ExactArgs(0),
		Run: func(cmd *cobra.Command, args []string) {
			// Instantiate store.
			s, err := store.New(config.Chat.Directory)
			cobra.CheckErr(err)

			// Parse a chat if relevant.opts.ChatID,
			var chat *store.Chat
			if opts.ChatID != "" {
				chat, err = s.Get(opts.ChatID)
				cobra.CheckErr(err)
			} else if opts.Continue {
				// Fetch the latest chat.
				chats, err := s.List(1)
				cobra.CheckErr(err)
				if len(chats) == 0 {
					cobra.CheckErr(fmt.Errorf("no chat to continue"))
				}
				chat = chats[0]
				opts.ChatID = chat.ID
			} else {
				opts.ChatID = uuid.New().String()[:8]
				chat = store.NewChat(opts.ChatID)
			}

			// Parse model and role.
			model, err := model.Parse(config, opts.Model)
			cobra.CheckErr(err)
			role, err := opts.Role.Parse()
			cobra.CheckErr(err)

			// Headers.
			roleName := "anon"
			if role != nil {
				roleName = role.Name
			}
			cli.Title("%s@%s[%s]", roleName, model.ID, opts.ChatID)

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
			if role != nil {
				message := openai.ChatCompletionMessage{
					Role:    openai.ChatMessageRoleSystem,
					Content: role.Description,
				}
				additionalMessages = append(additionalMessages, message)
			}

			// Print history.
			for _, message := range chat.Messages {
				if message.Role == openai.ChatMessageRoleUser {
					cli.UserInput("> %s\n", message.Content)
				}
				if message.Role == openai.ChatMessageRoleAssistant {
					if strings.Contains(message.Content, doNotSendToken) {
						trimmed := strings.TrimPrefix(message.Content, doNotSendToken)
						cli.UserCommand(trimmed + "\n")
						continue
					}
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
				for _, message := range chat.Messages {
					if !strings.Contains(message.Content, doNotSendToken) {
						messages = append(messages, message)
					}
				}
				messages = append(messages, userMessage)
				request := openai.ChatCompletionRequest{
					Model:    model.ID,
					Messages: messages,
					Stream:   true,
				}
				requestTokens, requestCost, err := model.CalculateRequestCost(messages...)
				cobra.CheckErr(err)
				if opts.ShowCost || (!config.CostThreshold.IsZero() && requestCost.GreaterThan(config.CostThreshold)) {
					cli.CostInfo("Request contains %d tokens costing $%s\n", requestTokens, requestCost.String())
					if !cli.QueryUser("continue") {
						return
					}
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

				matches := imagePromptRegexp.FindStringSubmatch(chatCompletionMessage.Content)
				if len(matches) == 2 {
					if !cli.QueryUser("Generate an image?") {
						continue
					}
					match := matches[1]
					cli.UserCommand("Generation started...\n")

					// Generate an image.
					request := openai.ImageRequest{
						Model:          openai.CreateImageModelDallE3,
						Quality:        opts.ImageQuality,
						Size:           opts.ImageSize,
						N:              opts.ImageNumber,
						Prompt:         match,
						ResponseFormat: openai.CreateImageResponseFormatURL,
					}
					response, err := openAIClient.CreateImage(ctx, request)
					if err != nil {
						cobra.CheckErr(fmt.Errorf("failed to created image: %v", err))
					}
					cli.UserCommand("Generation completed: %s\n", response.Data[0].URL)

					// Save response (with the `DoNotSend` token).
					chatCompletionMessage = openai.ChatCompletionMessage{
						Role:    openai.ChatMessageRoleAssistant,
						Content: doNotSendToken + response.Data[0].URL,
					}
					chat.Messages = append(chat.Messages, chatCompletionMessage)
					err = s.Write(chat)
					cobra.CheckErr(err)
				}
			}
		},
	}

	opts.FileInjection = file.GetOpts(cmd)
	opts.Role = role.GetOpts(cmd, config.Chat.DefaultRole, config.Chat.Roles)
	opts.Model = model.GetOpts(cmd, config.Chat.DefaultModel)
	cmd.Flags().StringVar(&opts.ChatID, "id", "", "specify a chat id. Defaults to latest one")
	cmd.Flags().BoolVarP(&opts.Embeddings, "embeddings", "e", false, "Use embeddings")
	cmd.Flags().BoolVar(&opts.ShowCost, "show-cost", false, "Show cost")
	cmd.Flags().BoolVarP(&opts.Continue, "continue", "c", false, "Continue previous chat")
	cmd.Flags().StringVar(&opts.ImageSize, "image-size", "1024x1024", "256x256, 512x512, 1024x1024, 1792x1024, 1024x1792")
	cmd.Flags().StringVar(&opts.ImageQuality, "image-quality", "hd", "hd, standard")
	cmd.Flags().IntVar(&opts.ImageNumber, "image-number", 1, "how many images to generate")

	cmd.AddCommand(newListCmd(config))
	return cmd
}
