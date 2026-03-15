package chat

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/google/uuid"
	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"
	"github.com/malonaz/core/go/aip"
	"github.com/malonaz/core/go/grpc/middleware"
	"github.com/spf13/cobra"

	"github.com/malonaz/sgpt/cli/tui"
	chatscreen "github.com/malonaz/sgpt/cli/tui/screen/chat"
	chatservicepb "github.com/malonaz/sgpt/genproto/chat/chat_service/v1"
	chatpb "github.com/malonaz/sgpt/genproto/chat/v1"
	"github.com/malonaz/sgpt/internal/configuration"
	"github.com/malonaz/sgpt/internal/file"
	"github.com/malonaz/sgpt/internal/role"
)

var models []*aipb.Model

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

			parsedRole, err := opts.Role.Parse()
			cobra.CheckErr(err)

			if opts.Model == "" {
				if parsedRole != nil && parsedRole.Model != "" {
					opts.Model = parsedRole.Model
				} else {
					opts.Model = config.Chat.DefaultModel
				}
			}
			opts.Model, err = config.ResolveModelAlias(opts.Model)
			cobra.CheckErr(err)

			if len(models) == 0 {
				cobra.CheckErr(fetchModels(ctx, aiClient))
			}

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

			var reasoningEffort aipb.ReasoningEffort
			switch strings.ToLower(opts.ReasoningEffort) {
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

			opts.FileInjection.Files = append(opts.FileInjection.Files, args...)
			files, err := file.Parse(opts.FileInjection)
			cobra.CheckErr(err)
			filePaths := make([]string, len(files))
			for i, f := range files {
				filePaths[i] = f.Path
			}

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

			additionalMessages := make([]*aipb.Message, 0, len(files)+1)
			additionalMessages = append(additionalMessages, ai.NewSystemMessage(ai.NewTextBlock(parsedRole.Prompt)))
			for _, f := range files {
				additionalMessages = append(additionalMessages, ai.NewUserMessage(ai.NewTextBlock(fmt.Sprintf("file %s: `%s`", f.Path, f.Content))))
			}

			chatOpts := chatscreen.Options{
				Model:           selectedModel,
				Role:            parsedRole,
				MaxTokens:       opts.MaxTokens,
				Temperature:     opts.Temperature,
				ReasoningEffort: reasoningEffort,
				EnableTools:     opts.EnableTools,
				ChatID:          opts.ChatID,
			}

			app := tui.NewApp(ctx, config, aiClient, chatClient, chat, chatOpts, additionalMessages, filePaths)

			p := tea.NewProgram(app, tea.WithContext(ctx))
			app.SetProgram(p)

			if _, err := p.Run(); err != nil {
				return fmt.Errorf("running chat: %w", err)
			}
			return nil
		},
	}

	opts.FileInjection = file.GetOpts(cmd)
	opts.Role = role.GetOpts(cmd, config.Chat.DefaultRole, config.Chat.Roles)
	cmd.Flags().StringVarP(&opts.Model, "model", "m", "", "Model name or alias")
	cmd.Flags().Int32Var(&opts.MaxTokens, "max-tokens", 0, "Maximum tokens to generate")
	cmd.Flags().Float64Var(&opts.Temperature, "temperature", 0, "Temperature (0.0-2.0)")
	cmd.Flags().StringVar(&opts.ChatID, "id", "", "Chat ID to resume")
	cmd.Flags().BoolVarP(&opts.Continue, "continue", "c", false, "Continue previous chat")
	cmd.Flags().StringVarP(&opts.ReasoningEffort, "think", "t", "", "Reasoning level (low, medium, high)")
	cmd.Flags().BoolVar(&opts.EnableTools, "tools", false, "Enable tool usage")

	cmd.RegisterFlagCompletionFunc("model", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(models) == 0 {
			fetchModels(cmd.Context(), aiClient)
		}
		return filterModels(models, toComplete), cobra.ShellCompDirectiveNoFileComp
	})

	return cmd
}

func filterModels(models []*aipb.Model, prefix string) []string {
	var names []string
	for _, model := range models {
		names = append(names, model.Name)
	}
	if prefix == "" {
		return names
	}
	lowerPrefix := strings.ToLower(prefix)
	var matches []string
	for _, name := range names {
		if strings.Contains(strings.ToLower(name), lowerPrefix) {
			matches = append(matches, name)
		}
	}
	return matches
}

func fetchModels(ctx context.Context, aiClient aiservicepb.AiServiceClient) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ctx = middleware.WithFieldMask(ctx, "next_page_token,models.name,models.ttt")
	listModelsRequest := &aiservicepb.ListModelsRequest{Parent: "providers/-"}
	fetchedModels, err := aip.Paginate[*aipb.Model](ctx, listModelsRequest, aiClient.ListModels)
	if err != nil {
		return err
	}
	models = fetchedModels
	return nil
}
