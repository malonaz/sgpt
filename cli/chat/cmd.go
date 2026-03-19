package chat

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"
	"github.com/spf13/cobra"

	"github.com/malonaz/sgpt/cli/tui"
	chatscreen "github.com/malonaz/sgpt/cli/tui/screen/chat"
	chatservicepb "github.com/malonaz/sgpt/genproto/chat/chat_service/v1"
	chatpb "github.com/malonaz/sgpt/genproto/chat/v1"
	"github.com/malonaz/sgpt/internal/configuration"
	"github.com/malonaz/sgpt/internal/file"
	"github.com/malonaz/sgpt/internal/role"
)

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

			selectedModel, err := resolveModel(ctx, aiClient, opts.Model)
			cobra.CheckErr(err)

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
				chat = &chatpb.Chat{
					Files: filePaths,
					Tags:  tags,
					Metadata: &chatpb.ChatMetadata{
						CurrentModel: opts.Model,
					},
				}
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
		cachedModels, _ := fetchModelsWithCache(cmd.Context(), aiClient, false)
		return filterModels(cachedModels, toComplete), cobra.ShellCompDirectiveNoFileComp
	})

	return cmd
}

func resolveModel(ctx context.Context, aiClient aiservicepb.AiServiceClient, modelName string) (*aipb.Model, error) {
	cachedModels, err := fetchModelsWithCache(ctx, aiClient, false)
	if err != nil {
		return nil, err
	}
	for _, model := range cachedModels {
		if model.Name == modelName {
			return model, nil
		}
	}
	cachedModels, err = fetchModelsWithCache(ctx, aiClient, true)
	if err != nil {
		return nil, err
	}
	for _, model := range cachedModels {
		if model.Name == modelName {
			return model, nil
		}
	}
	return nil, fmt.Errorf("model not found: %s", modelName)
}

func filterModels(cachedModels []*aipb.Model, prefix string) []string {
	var names []string
	for _, model := range cachedModels {
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
