package chat

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"
	"github.com/malonaz/core/go/grpc"
	"github.com/spf13/cobra"

	cliservice "github.com/malonaz/sgpt/cli/cli_service"
	"github.com/malonaz/sgpt/cli/tui"
	sgptservicepb "github.com/malonaz/sgpt/genproto/sgpt/sgpt_service/v1"
	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/configuration"
	"github.com/malonaz/sgpt/internal/debug"
	"github.com/malonaz/sgpt/internal/file"
	"github.com/malonaz/sgpt/internal/role"
	"github.com/malonaz/sgpt/internal/toolengine"
)

func NewCmd(
	config *sgptpb.Configuration,
	aiClient aiservicepb.AiServiceClient,
	chatClient sgptservicepb.SgptServiceClient,
	baseURLToGRPCConnection map[string]*grpc.Connection,
) *cobra.Command {
	var opts struct {
		FileInjection *file.InjectionOpts
		Role          *role.Opts
		Model         string
		MaxTokens     int32
		Temperature   float64
		Chat          string
		Continue      bool
		Tools         []string
		Debug         bool
	}

	cmd := &cobra.Command{
		Use: "chat",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 365*24*time.Hour)
			defer cancel()

			parsedRole, err := opts.Role.Parse()
			cobra.CheckErr(err)

			if opts.Model == "" {
				if parsedRole != nil && parsedRole.Model != "" {
					opts.Model = parsedRole.Model
				} else {
					opts.Model = config.Chat.DefaultModel
				}
			}
			opts.Model, err = configuration.ResolveModelAlias(config, opts.Model)
			cobra.CheckErr(err)

			selectedModel, err := resolveModel(ctx, aiClient, opts.Model)
			cobra.CheckErr(err)

			opts.FileInjection.Files = append(opts.FileInjection.Files, args...)

			// Append role-defined files.
			opts.FileInjection.Files = append(opts.FileInjection.Files, parsedRole.GetFiles()...)

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

			tools := append(opts.Tools, parsedRole.GetTools()...)

			var toolEngineManager *toolengine.Manager
			var toolEngineConfigurations []*sgptpb.ToolEngineConfiguration
			if len(tools) > 0 {
				toolEngineNameSet := map[string]struct{}{}
				for _, name := range tools {
					toolEngineNameSet[name] = struct{}{}
				}

				configToolEngineNameSet := map[string]struct{}{}
				for _, te := range config.ToolEngines {
					configToolEngineNameSet[te.GetName()] = struct{}{}
				}
				for name := range toolEngineNameSet {
					if _, ok := configToolEngineNameSet[name]; !ok {
						return fmt.Errorf("unknown tool engine %q", name)
					}
				}

				filteredConfig := *config
				for _, te := range config.ToolEngines {
					if _, ok := toolEngineNameSet[te.GetName()]; ok {
						toolEngineConfigurations = append(toolEngineConfigurations, te)
					}
				}
				filteredConfig.ToolEngines = toolEngineConfigurations
				toolEngineManager, err = toolengine.Initialize(ctx, &filteredConfig, baseURLToGRPCConnection)
				if err != nil {
					return fmt.Errorf("initializing tool engines: %w", err)
				}
				defer toolEngineManager.Close()
			}

			var chat *sgptpb.Chat
			if opts.Chat != "" {
				getChatRequest := &sgptservicepb.GetChatRequest{Name: opts.Chat}
				chat, err = chatClient.GetChat(ctx, getChatRequest)
				cobra.CheckErr(err)
			} else if opts.Continue {
				listChatsRequest := &sgptservicepb.ListChatsRequest{
					PageSize: 1,
					OrderBy:  "create_time desc",
				}
				listChatsResponse, err := chatClient.ListChats(ctx, listChatsRequest)
				cobra.CheckErr(err)
				if len(listChatsResponse.Chats) == 0 {
					cobra.CheckErr(fmt.Errorf("no chat to continue"))
				}
				chat = listChatsResponse.Chats[0]
				opts.Chat = chat.Name
			} else {
				chat = &sgptpb.Chat{
					Files: filePaths,
					Tags:  tags,
					Metadata: &sgptpb.ChatMetadata{
						CurrentModel: opts.Model,
					},
				}
			}

			additionalMessages := make([]*aipb.Message, 0, len(files)+1)
			additionalMessages = append(additionalMessages, ai.NewSystemMessage(ai.NewTextBlock(parsedRole.Prompt)))
			for _, c := range toolEngineConfigurations {
				additionalMessages = append(additionalMessages, ai.NewUserMessage(ai.NewTextBlock(c.Instructions)))
			}
			for _, f := range files {
				additionalMessages = append(additionalMessages, ai.NewUserMessage(ai.NewTextBlock(fmt.Sprintf("file %s: `%s`", f.Path, f.Content))))
			}

			service := &cliservice.Service{
				Config:                  config,
				AIClient:                aiClient,
				ChatClient:              chatClient,
				BaseURLToGRPCConnection: baseURLToGRPCConnection,
			}

			if opts.Debug {
				_, err := debug.Init(ctx)
				if err != nil {
					return fmt.Errorf("starting debug server: %w", err)
				}
			}

			params := cliservice.SessionParams{
				Model:              selectedModel,
				Role:               parsedRole,
				MaxTokens:          opts.MaxTokens,
				Temperature:        opts.Temperature,
				Chat:               opts.Chat,
				ToolEngineManager:  toolEngineManager,
				AdditionalMessages: additionalMessages,
				InjectedFiles:      filePaths,
			}

			app := tui.NewApp(ctx, service, chat, params)

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
	cmd.Flags().StringVar(&opts.Chat, "name", "", "Chat to resume")
	cmd.Flags().BoolVarP(&opts.Continue, "continue", "c", false, "Continue previous chat")
	cmd.Flags().StringSliceVar(&opts.Tools, "tool", nil, "Enable a specific tool engine by name (repeatable)")
	cmd.Flags().BoolVar(&opts.Debug, "debug", false, "Start a local debug log server")

	cmd.RegisterFlagCompletionFunc("model", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		cachedModels, _ := fetchModelsWithCache(cmd.Context(), aiClient, false)
		return filterModels(cachedModels, toComplete), cobra.ShellCompDirectiveNoFileComp
	})

	cmd.RegisterFlagCompletionFunc("tool", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		var names []string
		for _, te := range config.GetToolEngines() {
			name := te.GetName()
			if toComplete == "" || strings.Contains(strings.ToLower(name), strings.ToLower(toComplete)) {
				names = append(names, name)
			}
		}
		return names, cobra.ShellCompDirectiveNoFileComp
	})

	cmd.RegisterFlagCompletionFunc("role", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		var names []string
		for _, r := range config.Chat.GetRoles() {
			name := r.GetName()
			if toComplete == "" || strings.Contains(strings.ToLower(name), strings.ToLower(toComplete)) {
				names = append(names, name)
			}
			if alias := r.GetAlias(); alias != "" {
				if toComplete == "" || strings.Contains(strings.ToLower(alias), strings.ToLower(toComplete)) {
					names = append(names, alias)
				}
			}
		}
		return names, cobra.ShellCompDirectiveNoFileComp
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
