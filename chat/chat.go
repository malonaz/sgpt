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
	"github.com/spf13/cobra"

	"github.com/malonaz/sgpt/chat/store"
	"github.com/malonaz/sgpt/internal/cli"
	"github.com/malonaz/sgpt/internal/configuration"
	"github.com/malonaz/sgpt/internal/file"
	"github.com/malonaz/sgpt/internal/llm"
	"github.com/malonaz/sgpt/internal/role"
)

const streamTokenTimeout = 5 * time.Second
const doNotSendToken = "%@#$!@"

var imagePromptRegexp = regexp.MustCompile(`prompt\((.*?)\)`)

// NewCmd instantiates and returns the chat command.
func NewCmd(config *configuration.Config) *cobra.Command {
	var opts struct {
		FileInjection *file.InjectionOpts
		Role          *role.Opts
		LLM           *llm.Opts
		ChatID        string
		Embeddings    bool
		Continue      bool
		ImageConfirm  bool
		ImageSize     string
		ImageQuality  string
		ImageNumber   int
	}
	cmd := &cobra.Command{
		Use:   "chat",
		Short: "Back and forth chat",
		Long:  "Back and forth chat",
		Run: func(cmd *cobra.Command, args []string) {
			// Parse model and role.
			role, err := opts.Role.Parse()
			cobra.CheckErr(err)

			if opts.LLM.Model == "" {
				if role != nil && role.Model != "" {
					opts.LLM.Model = role.Model
				} else {
					opts.LLM.Model = config.Chat.DefaultModel
				}
			}

			opts.FileInjection.Files = append(opts.FileInjection.Files, args...)
			llmClient, model, provider, err := llm.NewClient(config, opts.LLM)
			cobra.CheckErr(err)

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

			// Headers.
			roleName := "anon"
			if role != nil {
				roleName = role.Name
			}
			cli.Title("%s@%s/%s[%s]", roleName, provider.Name, model.Name, opts.ChatID)

			// Inject files.
			files, err := file.Parse(opts.FileInjection)
			cobra.CheckErr(err)
			additionalMessages := make([]*llm.Message, 0, len(files))
			for _, file := range files {
				message := &llm.Message{
					Role:    llm.SystemRole,
					Content: fmt.Sprintf("file %s: `%s`", file.Path, file.Content),
				}
				additionalMessages = append(additionalMessages, message)
				cli.FileInfo("injecting file #%d: %s\n", len(additionalMessages), file.Path)
			}

			// Inject role.
			if role != nil {
				message := &llm.Message{
					Role:    llm.SystemRole,
					Content: role.Prompt,
				}
				additionalMessages = append(additionalMessages, message)
			}

			// Print history.
			for _, message := range chat.Messages {
				if message.Role == llm.UserRole {
					cli.UserInput("> %s\n", message.Content)
				}
				if message.Role == llm.AssistantRole {
					if strings.Contains(message.Content, doNotSendToken) {
						trimmed := strings.TrimPrefix(message.Content, doNotSendToken)
						cli.UserCommand(trimmed + "\n")
						continue
					}
					cli.AIOutput(message.Content + "\n")
				}
			}

			ctx := context.Background()
			for {
				// Query user for prompt.
				text, err := cli.PromptUser()
				cobra.CheckErr(err)
				// Quick feedback so user knows query has been submitted.
				cli.AIOutput("SGPT: ")

				// Set cancelable context with timeout.
				ctx, cancel := context.WithTimeout(ctx, time.Duration(provider.RequestTimeout)*time.Second)
				defer cancel()

				// If relevant, fetch embeddings.
				var embeddingMessages []*llm.Message
				if opts.Embeddings {
					embeddingMessages, err = getEmbeddingMessages(ctx, config, llmClient, text)
					cobra.CheckErr(err)
				}

				// Create open AI request.
				userMessage := &llm.Message{
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
				request := &llm.CreateTextGenerationRequest{
					Model:     model.Name,
					Messages:  messages,
					MaxTokens: model.MaxTokens,
				}

				// Initiate Open AI stream.
				stream, err := llmClient.CreateTextGeneration(ctx, request)
				cobra.CheckErr(err)
				defer stream.Close()
				tokenChannel, errorChannel := pipeStream(stream)

				// Process Open AI stream.
				interruptSignalChannel := make(chan os.Signal, 1)
				signal.Notify(interruptSignalChannel, os.Interrupt)
				interrupted := false
				chatCompletionMessage := &llm.Message{Role: llm.AssistantRole}
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

				if !interrupted {
					// Append the response content to our history.
					chat.Messages = append(chat.Messages, userMessage, chatCompletionMessage)
					// Save chat.
					err := s.Write(chat)
					cobra.CheckErr(err)
				}

				matches := imagePromptRegexp.FindStringSubmatch(chatCompletionMessage.Content)
				if len(matches) == 2 {
					if config.ImageProvider == nil {
						cobra.CheckErr(fmt.Errorf("need to define an open ai image provider in the configuration.chat section"))
					}

					if opts.ImageConfirm && !cli.QueryUser("Generate an image?") {
						continue
					}
					match := matches[1]
					cli.UserCommand("Generation started...")

					// Generate an image.
					openAIClient := llm.NewOpenAIClient(config.ImageProvider.APIKey, config.ImageProvider.APIHost)
					request := openai.ImageRequest{
						Model:          config.ImageProvider.Model,
						Quality:        opts.ImageQuality,
						Size:           opts.ImageSize,
						N:              opts.ImageNumber,
						Prompt:         match,
						ResponseFormat: openai.CreateImageResponseFormatURL,
					}
					response, err := openAIClient.Get().CreateImage(ctx, request)
					if err != nil {
						cobra.CheckErr(fmt.Errorf("failed to created image: %v", err))
					}
					cli.UserCommand("%s\n", response.Data[0].URL)

					// Save response (with the `DoNotSend` token).
					chatCompletionMessage = &llm.Message{
						Role:    llm.AssistantRole,
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
	opts.LLM = llm.GetOpts(cmd)
	cmd.Flags().StringVar(&opts.ChatID, "id", "", "specify a chat id. Defaults to latest one")
	cmd.Flags().BoolVarP(&opts.Embeddings, "embeddings", "e", false, "Use embeddings")
	cmd.Flags().BoolVarP(&opts.Continue, "continue", "c", false, "Continue previous chat")
	cmd.Flags().StringVar(&opts.ImageSize, "image-size", "1024x1024", "256x256, 512x512, 1024x1024, 1792x1024, 1024x1792")
	cmd.Flags().StringVar(&opts.ImageQuality, "image-quality", "hd", "hd, standard")
	cmd.Flags().IntVar(&opts.ImageNumber, "image-number", 1, "how many images to generate")
	cmd.Flags().BoolVar(&opts.ImageConfirm, "image-confirm", false, "If true, we confirm before generating each image")
	return cmd
}
