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

	"github.com/malonaz/sgpt/internal/cli"
	"github.com/malonaz/sgpt/internal/configuration"
	"github.com/malonaz/sgpt/internal/file"
	"github.com/malonaz/sgpt/internal/llm"
	"github.com/malonaz/sgpt/internal/role"
	"github.com/malonaz/sgpt/store"
)

const streamTokenTimeout = 5 * time.Second
const doNotSendToken = "%@#$!@"

var imagePromptRegexp = regexp.MustCompile(`prompt\((.*?)\)`)

// NewCmd instantiates and returns the chat command.
func NewCmd(config *configuration.Config, s *store.Store) *cobra.Command {
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
			ctx := cmd.Context()

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
			files, err := file.Parse(opts.FileInjection)
			cobra.CheckErr(err)
			filePaths := make([]string, len(files))
			for i, f := range files {
				filePaths[i] = f.Path
			}
			githubRepoSet := map[string]struct{}{}
			for _, filePath := range filePaths {
				githubRepo, err := file.GetGitHubRepo(filePath)
				cobra.CheckErr(err)
				githubRepoSet[githubRepo] = struct{}{}
			}
			githubRepos := make([]string, 0, len(githubRepoSet))
			for githubRepo := range githubRepoSet {
				githubRepos = append(githubRepos, githubRepo)
			}
			llmClient, model, provider, err := llm.NewClient(config, opts.LLM)
			cobra.CheckErr(err)

			// Parse a chat if relevant.opts.ChatID,
			var chat *store.Chat
			now := time.Now().UnixMicro()
			if opts.ChatID != "" {
				chat, err = s.GetChat(opts.ChatID)
				cobra.CheckErr(err)
			} else if opts.Continue {
				// Fetch the latest chat.
				listChatsRequest := &store.ListChatsRequest{
					PageSize: 1,
				}
				listChatsResponse, err := s.ListChats(listChatsRequest)
				cobra.CheckErr(err)
				if len(listChatsResponse.Chats) == 0 {
					cobra.CheckErr(fmt.Errorf("no chat to continue"))
				}
				chat = listChatsResponse.Chats[0]
				opts.ChatID = chat.ID
			} else {
				opts.ChatID = uuid.New().String()[:8]
				chat = &store.Chat{
					ID:    opts.ChatID,
					Files: filePaths,
				}
			}
			chat.UpdateTimestamp = now
			chat.Files = append(chat.Files, filePaths...)
			chat.Tags = append(chat.Tags, githubRepos...)

			// Headers.
			roleName := "anon"
			if role != nil {
				roleName = role.Name
			}
			cli.Title("%s@%s/%s[%s]", roleName, provider.Name, model.Name, opts.ChatID)

			// Inject files.
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

			for {
				// Query user for prompt.
				text, err := cli.PromptUser()
				cobra.CheckErr(err)

				// Quick feedback so user knows query has been submitted.
				cli.UserCommand("Generating...")

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
					Model:          model.Name,
					Messages:       messages,
					MaxTokens:      model.MaxTokens,
					ThinkingTokens: model.ThinkingTokens,
				}

				// Initiate Open AI stream.
				stream, err := llmClient.CreateTextGeneration(ctx, request)
				cobra.CheckErr(err)
				defer stream.Close()
				eventChannel, errorChannel := pipeStream(stream)

				// Process Open AI stream.
				interruptSignalChannel := make(chan os.Signal, 1)
				signal.Notify(interruptSignalChannel, os.Interrupt)
				interrupted := false
				chatCompletionMessage := &llm.Message{Role: llm.AssistantRole}
				firstReasoningToken := true
				firstToken := true
				for {
					streamEnded := false
					select {
					case <-interruptSignalChannel:
						// We've detected an interrupt, kill the stream.
						cli.UserCommand("#Interrupted")
						stream.Close()
						interrupted = true
						streamEnded = true
					case event := <-eventChannel:
						if event.Token != "" {
							if firstToken {
								cli.AIOutput("\n")
								firstToken = false
							}
							chatCompletionMessage.Content += event.Token
							cli.AIOutput(event.Token)
						}
						if event.ReasoningToken != "" {
							if firstReasoningToken {
								cli.AIThought("\n")
								firstReasoningToken = false
							}
							content := strings.ReplaceAll(event.ReasoningToken, "%", "%%")
							cli.AIThought(content)
						}
					case err := <-errorChannel:
						if errors.Is(err, io.EOF) {
							streamEnded = true
						} else {
							cobra.CheckErr(err)
						}
					}
					if streamEnded {
						// Stop listening to signal.
						signal.Stop(interruptSignalChannel)
						break
					}
				}
				cli.AIOutput("\n")
				if interrupted {
					continue
				}

				// Append the response content to our history.
				chat.Messages = append(chat.Messages, userMessage, chatCompletionMessage)

				matches := imagePromptRegexp.FindStringSubmatch(chatCompletionMessage.Content)
				if len(matches) == 2 {
					if config.ImageProvider == nil {
						cobra.CheckErr(fmt.Errorf("need to define an open ai image provider in the configuration.chat section"))
					}

					if opts.ImageConfirm && !cli.QueryUser("Generate an image?") {
						continue
					}
					chat.Tags = append(chat.Tags, "image")
					match := matches[1]
					cli.UserCommand("Image generation started...")

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
				}

				if chat.CreationTimestamp == 0 {
					chat.CreationTimestamp = now
					chat.UpdateTimestamp = now
					createChatRequest := &store.CreateChatRequest{
						Chat: chat,
					}
					_, err := s.CreateChat(createChatRequest)
					cobra.CheckErr(err)
					go func() {
						if err := generateChatSummary(ctx, config, s, chat); err != nil {
							fmt.Errorf("generating summary for chat %s: %v", chat.ID, err)
						}
					}()
				} else {
					// Save chat.
					updateChatRequest := &store.UpdateChatRequest{
						Chat:       chat,
						UpdateMask: []string{"messages", "files", "tags"},
					}
					err = s.UpdateChat(updateChatRequest)
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

// generateChatSummary creates a title/summary for the chat using the specified model
func generateChatSummary(ctx context.Context, config *configuration.Config, s *store.Store, chat *store.Chat) error {
	if config.Chat.SummaryModel == "" {
		return nil
	}
	if len(chat.Messages) < 2 {
		return fmt.Errorf("expected at least 2 messages, found %d", len(chat.Messages))
	}
	if chat.Messages[0].Role != llm.UserRole || chat.Messages[1].Role != llm.AssistantRole {
		return fmt.Errorf("expected first message to be user role and second message to be assistant role")
	}
	messages := chat.Messages[:2]

	// Create a new LLM client for summary generation
	summaryOpts := &llm.Opts{Model: config.Chat.SummaryModel}
	summaryClient, model, _, err := llm.NewClient(config, summaryOpts)
	if err != nil {
		return fmt.Errorf("failed to create summary client: %w", err)
	}

	// Create prompt for summary generation
	summaryPrompt := "Generate a brief, concise title (max 6 words) for this conversation so far. YOU MUST ALWAYS OUTPUT SOMETHING."
	summaryMessages := append(messages, &llm.Message{Role: llm.UserRole, Content: summaryPrompt})

	// Create request
	request := &llm.CreateTextGenerationRequest{
		Model:          model.Name,
		Messages:       summaryMessages,
		MaxTokens:      50, // Should be enough for a short title
		ThinkingTokens: model.ThinkingTokens,
	}

	// Get response
	stream, err := summaryClient.CreateTextGeneration(ctx, request)
	if err != nil {
		return fmt.Errorf("failed to generate summary: %w", err)
	}
	defer stream.Close()

	// Collect the complete response
	var summary strings.Builder
	for {
		event, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("error receiving summary stream: %w", err)
		}
		summary.WriteString(event.Token)
	}

	// Clean up the summary (remove quotes, newlines, etc.)
	cleanSummary := strings.TrimSpace(summary.String())
	cleanSummary = strings.Trim(cleanSummary, `"'`)
	cleanSummary = strings.ReplaceAll(cleanSummary, "\n", " ")

	if cleanSummary != "" {
		chat.Title = &cleanSummary
		updateChatRequest := &store.UpdateChatRequest{
			Chat:       chat,
			UpdateMask: []string{"title"},
		}
		if err := s.UpdateChat(updateChatRequest); err != nil {
			return fmt.Errorf("updating chat title: %w", err)
		}
	}

	return nil
}
