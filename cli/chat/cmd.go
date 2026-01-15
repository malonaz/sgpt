package chat

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"
	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"
	"github.com/malonaz/core/go/aip"
	"github.com/malonaz/core/go/grpc/interceptor"
	"github.com/spf13/cobra"

	"github.com/malonaz/sgpt/cli/tui"
	chatservicepb "github.com/malonaz/sgpt/genproto/chat/chat_service/v1"
	chatpb "github.com/malonaz/sgpt/genproto/chat/v1"
	"github.com/malonaz/sgpt/internal/configuration"
	"github.com/malonaz/sgpt/internal/file"
	"github.com/malonaz/sgpt/internal/role"
	"github.com/malonaz/sgpt/internal/types"
)

var models []*aipb.Model

// NewCmd instantiates and returns the chat command.
func NewCmd(config *configuration.Config, aiClient aiservicepb.AiServiceClient, chatClient chatservicepb.ChatServiceClient) *cobra.Command {
	var opts struct {
		FileInjection   *file.InjectionOpts
		Role            *role.Opts
		Model           string
		MaxTokens       int32
		Temperature     float64
		ChatID          string
		Continue        bool
		ReasoningEffort string
		EnableTools     bool
	}
	cmd := &cobra.Command{
		Use: "chat",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			// Parse model and role.
			parsedRole, err := opts.Role.Parse()
			cobra.CheckErr(err)

			// Set defaults
			if opts.Model == "" {
				if parsedRole != nil && parsedRole.Model != "" {
					opts.Model = parsedRole.Model
				} else {
					opts.Model = config.Chat.DefaultModel
				}
			}

			// Resolve model alias
			opts.Model, err = config.ResolveModelAlias(opts.Model)
			cobra.CheckErr(err)

			// Fetch models if not already cached
			if len(models) == 0 {
				err := fetchModels(ctx, aiClient)
				cobra.CheckErr(err)
			}

			// Find the model by name
			var selectedModel *aipb.Model
			for _, model := range models {
				if model.Name == opts.Model {
					selectedModel = model
					break
				}
			}
			if selectedModel == nil {
				return fmt.Errorf("model not found: %s", opts.Model)
			}

			// Parse reasoning effort.
			var reasoningEffort aipb.ReasoningEffort
			opts.ReasoningEffort = strings.ToLower(opts.ReasoningEffort)
			switch opts.ReasoningEffort {
			case "":
			case "low", "l":
				reasoningEffort = aipb.ReasoningEffort_REASONING_EFFORT_LOW
			case "medium", "m":
				reasoningEffort = aipb.ReasoningEffort_REASONING_EFFORT_MEDIUM
			case "high", "h":
				reasoningEffort = aipb.ReasoningEffort_REASONING_EFFORT_HIGH
			default:
				return fmt.Errorf("unknown reasoning effort %s", opts.ReasoningEffort)
			}

			// Parse files
			opts.FileInjection.Files = append(opts.FileInjection.Files, args...)
			files, err := file.Parse(opts.FileInjection)
			cobra.CheckErr(err)
			filePaths := make([]string, len(files))
			for i, f := range files {
				filePaths[i] = f.Path
			}

			// Process tags.
			var tags []string
			githubRepoSet := map[string]struct{}{}
			for _, filePath := range filePaths {
				githubRepo, err := file.GetGitHubRepo(filePath)
				cobra.CheckErr(err)
				githubRepoSet[githubRepo] = struct{}{}
			}
			for githubRepo := range githubRepoSet {
				tags = append(tags, githubRepo)
			}

			// Parse or create chat
			var chat *chatpb.Chat
			if opts.ChatID != "" {
				getChatRequest := &chatservicepb.GetChatRequest{Name: opts.ChatID}
				chat, err = chatClient.GetChat(ctx, getChatRequest)
				cobra.CheckErr(err)
			} else if opts.Continue {
				listChatsRequest := &chatservicepb.ListChatsRequest{
					PageSize: 1,
					OrderBy:  "create_time desc",
				}
				listChatsResponse, err := chatClient.ListChats(ctx, listChatsRequest)
				cobra.CheckErr(err)
				if len(listChatsResponse.Chats) == 0 {
					cobra.CheckErr(fmt.Errorf("no chat to continue"))
				}
				chat = listChatsResponse.Chats[0]
				opts.ChatID = chat.Name
			} else {
				createChatRequest := &chatservicepb.CreateChatRequest{
					RequestId: uuid.New().String(),
					ChatId:    uuid.New().String()[:8],
					Chat: &chatpb.Chat{
						Files: filePaths,
						Tags:  tags,
						Metadata: &chatpb.ChatMetadata{
							CurrentModel: opts.Model,
						},
					},
				}
				chat, err = chatClient.CreateChat(ctx, createChatRequest)
				cobra.CheckErr(err)
				opts.ChatID = chat.Name
			}

			// Build additional messages (files + role)
			additionalMessages := make([]*aipb.Message, 0, len(files)+1)
			// Inject role
			message := ai.NewSystemMessage(&aipb.SystemMessage{Content: parsedRole.Prompt})
			additionalMessages = append(additionalMessages, message)

			// Inject files
			for _, f := range files {
				message := ai.NewUserMessage(&aipb.UserMessage{Content: fmt.Sprintf("file %s: `%s`", f.Path, f.Content)})
				additionalMessages = append(additionalMessages, message)
			}

			// Create chat options
			chatOpts := types.ChatOptions{
				Model:           selectedModel,
				Role:            parsedRole,
				MaxTokens:       opts.MaxTokens,
				Temperature:     opts.Temperature,
				ReasoningEffort: reasoningEffort,
				EnableTools:     opts.EnableTools,
				ChatID:          opts.ChatID,
			}

			// Create the model
			m, err := tui.New(ctx, config, aiClient, chatClient, chat, chatOpts, additionalMessages, filePaths)
			if err != nil {
				return err
			}

			// Create the Bubble Tea program
			p := tea.NewProgram(
				m,
				tea.WithAltScreen(),
				tea.WithContext(ctx),
				tea.WithFilter(m.Filter()),
				tea.WithMouseCellMotion(),
				tea.WithReportFocus(),
			)

			// Set the program reference for async message sending
			m.SetProgram(p)

			if _, err := p.Run(); err != nil {
				return fmt.Errorf("error running chat: %w", err)
			}

			return nil
		},
	}

	opts.FileInjection = file.GetOpts(cmd)
	opts.Role = role.GetOpts(cmd, config.Chat.DefaultRole, config.Chat.Roles)
	cmd.Flags().StringVarP(&opts.Model, "model", "m", "", "Model name or alias to use (e.g., 'o' for gpt-4o, '4' for gpt-4)")
	cmd.Flags().Int32Var(&opts.MaxTokens, "max-tokens", 0, "Maximum tokens to generate")
	cmd.Flags().Float64Var(&opts.Temperature, "temperature", 0, "Temperature (0.0-2.0)")
	cmd.Flags().StringVar(&opts.ChatID, "id", "", "specify a chat id")
	cmd.Flags().BoolVarP(&opts.Continue, "continue", "c", false, "Continue previous chat")
	cmd.Flags().StringVarP(&opts.ReasoningEffort, "think", "t", "", "Specify a reasoning level (LOW, MEDIUM, HIGH)")
	cmd.Flags().BoolVar(&opts.EnableTools, "tools", false, "Enable tool usage (shell commands, etc)")

	cmd.RegisterFlagCompletionFunc("model", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(models) == 0 {
			err := fetchModels(cmd.Context(), aiClient)
			cobra.CheckErr(err)
		}
		return filterModels(models, toComplete), cobra.ShellCompDirectiveNoFileComp
	})

	return cmd
}

func filterModels(models []*aipb.Model, prefix string) []string {
	var modelNames []string
	for _, model := range models {
		modelNames = append(modelNames, model.Name)
	}

	if prefix == "" {
		return modelNames
	}

	var matches []string
	lowerPrefix := strings.ToLower(prefix)

	for _, name := range modelNames {
		if strings.Contains(strings.ToLower(name), lowerPrefix) {
			matches = append(matches, name)
		}
	}

	return matches
}

func fetchModels(ctx context.Context, aiClient aiservicepb.AiServiceClient) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	ctx = interceptor.WithFieldMask(ctx, "next_page_token,models.name,models.ttt")

	listModelsRequest := &aiservicepb.ListModelsRequest{
		Parent: "providers/-",
	}
	fetchedModels, err := aip.Paginate[*aipb.Model](ctx, listModelsRequest, aiClient.ListModels)
	if err != nil {
		return err
	}
	models = fetchedModels
	return nil
}
