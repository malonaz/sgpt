package chat

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/google/uuid"
	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"

	"github.com/malonaz/sgpt/internal/cli"
	"github.com/malonaz/sgpt/internal/configuration"
	"github.com/malonaz/sgpt/internal/file"
	"github.com/malonaz/sgpt/internal/role"
	"github.com/malonaz/sgpt/store"
)

// NewCmd instantiates and returns the chat command.
func NewCmd(config *configuration.Config, s *store.Store, aiClient aiservicepb.AiClient) *cobra.Command {
	var opts struct {
		FileInjection *file.InjectionOpts
		Role          *role.Opts
		Model         string
		MaxTokens     int64
		Temperature   float64
		ChatID        string
		Continue      bool
		Reasoning     string
	}
	cmd := &cobra.Command{
		Use: "chat",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			// Parse model and role.
			role, err := opts.Role.Parse()
			cobra.CheckErr(err)

			// Set defaults
			if opts.Model == "" {
				if role != nil && role.Model != "" {
					opts.Model = role.Model
				} else {
					opts.Model = config.Chat.DefaultModel
				}
			}

			// Resolve model alias
			opts.Model, err = config.ResolveModelAlias(opts.Model)
			cobra.CheckErr(err)

			// Parse files
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

			// Parse or create chat
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

			// Headers
			roleName := "anon"
			if role != nil {
				roleName = role.Name
			}
			cli.Title("%s | %s | %s", opts.Model, roleName, opts.ChatID)

			// Build additional messages (files + role)
			additionalMessages := make([]*aipb.Message, 0, len(files)+1)

			// Inject files
			for _, file := range files {
				message := &aipb.Message{
					Role:    aipb.Role_ROLE_SYSTEM,
					Content: fmt.Sprintf("file %s: `%s`", file.Path, file.Content),
				}
				additionalMessages = append(additionalMessages, message)
				cli.FileInfo("injecting file #%d: %s\n", len(additionalMessages), file.Path)
			}

			// Inject role
			if role != nil {
				message := &aipb.Message{
					Role:    aipb.Role_ROLE_SYSTEM,
					Content: role.Prompt,
				}
				additionalMessages = append(additionalMessages, message)
			}

			// Print history
			for _, message := range chat.Messages {
				switch message.Role {
				case aipb.Role_ROLE_USER:
					cli.UserInput("> %s\n", message.Content)
				case aipb.Role_ROLE_ASSISTANT:
					if message.Reasoning != "" {
						cli.AIThought(message.Reasoning + "\n")
					}
					cli.AIOutput(message.Content + "\n")
				}
			}

			for {
				// Query user for prompt
				text, err := cli.PromptUser()
				cobra.CheckErr(err)

				// Quick feedback
				cli.UserCommand("Generating...")

				// Create user message
				userMessage := &aipb.Message{
					Role:    aipb.Role_ROLE_USER,
					Content: text,
				}

				// Build messages for request
				messages := append([]*aipb.Message{}, additionalMessages...)
				messages = append(messages, chat.Messages...)
				messages = append(messages, userMessage)

				reasoningEffortInt, ok := aipb.ReasoningEffort_value["REASONING_EFFORT_"+strings.ToUpper(opts.Reasoning)]
				if !ok {
					return fmt.Errorf("unknown reasoning  %s", opts.Reasoning)
				}

				// Create request
				request := &aiservicepb.TextToTextStreamRequest{
					Model:    opts.Model,
					Messages: messages,
					Configuration: &aiservicepb.TextToTextConfiguration{
						MaxTokens:       opts.MaxTokens,
						Temperature:     opts.Temperature,
						ReasoningEffort: aipb.ReasoningEffort(reasoningEffortInt),
					},
				}

				// Initiate stream
				stream, err := aiClient.TextToTextStream(ctx, request)
				cobra.CheckErr(err)

				// Process stream
				interruptSignalChannel := make(chan os.Signal, 1)
				signal.Notify(interruptSignalChannel, os.Interrupt)
				interrupted := false
				assistantMessage := &aipb.Message{Role: aipb.Role_ROLE_ASSISTANT}
				firstToken := true

				for {
					streamEnded := false
					select {
					case <-interruptSignalChannel:
						// Detected interrupt
						cli.UserCommand("#Interrupted")
						interrupted = true
						streamEnded = true
					default:
						response, err := stream.Recv()
						if err != nil {
							if errors.Is(err, io.EOF) {
								streamEnded = true
							} else {
								cobra.CheckErr(err)
							}
							break
						}

						switch content := response.Content.(type) {
						case *aiservicepb.TextToTextStreamResponse_ContentChunk:
							if firstToken {
								cli.AIOutput("\n")
								firstToken = false
							}
							assistantMessage.Content += content.ContentChunk
							cli.AIOutput(content.ContentChunk)

						case *aiservicepb.TextToTextStreamResponse_ReasoningChunk:
							if assistantMessage.Reasoning == "" {
								cli.AIThought("\n")
							}
							assistantMessage.Reasoning += content.ReasoningChunk
							escapedContent := strings.ReplaceAll(content.ReasoningChunk, "%", "%%")
							cli.AIThought(escapedContent)

						case *aiservicepb.TextToTextStreamResponse_StopReason:
							// Stream ended
							streamEnded = true

						case *aiservicepb.TextToTextStreamResponse_ToolCall:
							// Handle tool calls
							assistantMessage.ToolCalls = append(assistantMessage.ToolCalls, content.ToolCall)

						case *aiservicepb.TextToTextStreamResponse_ModelUsage:
							// Could log usage metrics if needed

						case *aiservicepb.TextToTextStreamResponse_GenerationMetrics:
							// Could log generation metrics if needed
						}
					}

					if streamEnded {
						signal.Stop(interruptSignalChannel)
						break
					}
				}

				cli.AIOutput("\n")

				if interrupted {
					continue
				}

				// Append messages to history
				chat.Messages = append(chat.Messages, userMessage, assistantMessage)

				// Save chat
				if chat.CreationTimestamp == 0 {
					chat.CreationTimestamp = now
					chat.UpdateTimestamp = now
					createChatRequest := &store.CreateChatRequest{
						Chat: chat,
					}
					_, err := s.CreateChat(createChatRequest)
					cobra.CheckErr(err)

					// Generate summary asynchronously
					go func() {
						if err := generateChatSummary(ctx, config, s, aiClient, chat); err != nil {
							fmt.Printf("error generating summary for chat %s: %v\n", chat.ID, err)
						}
					}()
				} else {
					updateChatRequest := &store.UpdateChatRequest{
						Chat:       chat,
						UpdateMask: []string{"messages", "files", "tags"},
					}
					err = s.UpdateChat(updateChatRequest)
					cobra.CheckErr(err)
				}
			}
			return nil
		},
	}

	opts.FileInjection = file.GetOpts(cmd)
	opts.Role = role.GetOpts(cmd, config.Chat.DefaultRole, config.Chat.Roles)
	cmd.Flags().StringVarP(&opts.Model, "model", "m", "", "Model name or alias to use (e.g., 'o' for gpt-4o, '4' for gpt-4)")
	cmd.Flags().Int64Var(&opts.MaxTokens, "max-tokens", 0, "Maximum tokens to generate")
	cmd.Flags().Float64Var(&opts.Temperature, "temperature", 0, "Temperature (0.0-2.0)")
	cmd.Flags().StringVar(&opts.ChatID, "id", "", "specify a chat id")
	cmd.Flags().BoolVarP(&opts.Continue, "continue", "c", false, "Continue previous chat")
	cmd.Flags().StringVar(&opts.Reasoning, "reason", "unspecified", "Specify a reasoning level (LOW, MEDIUM, HIGH)")

	return cmd
}
