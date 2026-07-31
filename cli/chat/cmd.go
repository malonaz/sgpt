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

	"github.com/malonaz/sgpt/cli/tui"
	sgptservicepb "github.com/malonaz/sgpt/genproto/sgpt/sgpt_service/v1"
	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/debug"
	"github.com/malonaz/sgpt/internal/file"
	"github.com/malonaz/sgpt/internal/role"
	"github.com/malonaz/sgpt/internal/session"
	"github.com/malonaz/sgpt/internal/store"
	"github.com/malonaz/sgpt/internal/tool"
	"github.com/malonaz/sgpt/internal/tool/agent"
	"github.com/malonaz/sgpt/internal/tool/diff"
	toolio "github.com/malonaz/sgpt/internal/tool/io"
	"github.com/malonaz/sgpt/internal/tool/rpc"
	"github.com/malonaz/sgpt/internal/tool/shell"
)

func NewCmd(
	config *sgptpb.Configuration,
	aiClient aiservicepb.AiServiceClient,
	chatClient sgptservicepb.SgptServiceClient,
	baseURLToGRPCConnection map[string]*grpc.Connection,
) *cobra.Command {
	chatStore := store.New(config, aiClient, chatClient)

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
			if opts.Debug {
				if _, err := debug.Init(ctx); err != nil {
					return fmt.Errorf("starting debug server: %w", err)
				}
			}

			parsedRole, err := opts.Role.Parse()
			cobra.CheckErr(err)

			if opts.Model == "" {
				if parsedRole != nil && parsedRole.Model != "" {
					opts.Model = parsedRole.Model
				} else {
					opts.Model = config.Chat.DefaultModel
				}
			}
			selectedModel, err := chatStore.ResolveModel(ctx, opts.Model)
			cobra.CheckErr(err)

			opts.FileInjection.Files = append(opts.FileInjection.Files, args...)
			opts.FileInjection.Files = append(opts.FileInjection.Files, parsedRole.GetFiles()...)
			files, err := file.Parse(opts.FileInjection)
			cobra.CheckErr(err)
			filePaths := make([]string, len(files))
			for i, parsedFile := range files {
				filePaths[i] = parsedFile.Path
			}

			// Tag the chat with the GitHub repos its files belong to.
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

			agentTool := &agent.Tool{}

			// buildRegistry is shared by the root chat and every sub-agent
			// launch, so sub-agents get the full tool surface.
			buildRegistry := func(ctx context.Context, toolNames []string) (*tool.Registry, []*sgptpb.ToolEngineConfiguration, error) {
				registry := tool.NewRegistry()
				registry.Register(tool.HandlerIDShell, &shell.Tool{})
				registry.Register(tool.HandlerIDReadFiles, &toolio.ReadFilesTool{})
				registry.Register(tool.HandlerIDDiff, &diff.Tool{})
				registry.Register(tool.HandlerIDReplace, &toolio.ReplaceTool{})
				// Same instance everywhere: sub-agents can spawn sub-agents.
				registry.Register(tool.HandlerIDAgent, agentTool)

				// Partition: built-in tools are advertised directly; everything
				// else must be a configured tool engine.
				internalToolNameSet := map[string]struct{}{}
				toolEngineNameSet := map[string]struct{}{}
				for _, name := range toolNames {
					if _, ok := tool.Builtin(name); ok {
						internalToolNameSet[name] = struct{}{}
						continue
					}
					toolEngineNameSet[name] = struct{}{}
				}
				for name := range internalToolNameSet {
					internalTool, _ := tool.Builtin(name)
					registry.AddTools(internalTool)
				}

				var toolEngineConfigurations []*sgptpb.ToolEngineConfiguration
				if len(toolEngineNameSet) > 0 {
					configuredToolEngineNameSet := map[string]struct{}{}
					for _, toolEngineConfiguration := range config.ToolEngines {
						configuredToolEngineNameSet[toolEngineConfiguration.GetName()] = struct{}{}
					}
					for name := range toolEngineNameSet {
						if _, ok := configuredToolEngineNameSet[name]; !ok {
							return nil, nil, fmt.Errorf("unknown tool engine %q", name)
						}
					}

					filteredConfiguration := *config
					for _, toolEngineConfiguration := range config.ToolEngines {
						if _, ok := toolEngineNameSet[toolEngineConfiguration.GetName()]; ok {
							toolEngineConfigurations = append(toolEngineConfigurations, toolEngineConfiguration)
						}
					}
					filteredConfiguration.ToolEngines = toolEngineConfigurations
					toolEngineManager, err := rpc.Initialize(ctx, &filteredConfiguration, baseURLToGRPCConnection)
					if err != nil {
						return nil, nil, fmt.Errorf("initializing tool engines: %w", err)
					}
					registry.Register(tool.HandlerIDEngine, toolEngineManager)
					registry.AddToolSets(toolEngineManager.GetToolSets()...)
				}
				return registry, toolEngineConfigurations, nil
			}

			toolNames := append(opts.Tools, parsedRole.GetTools()...)
			registry, toolEngineConfigurations, err := buildRegistry(ctx, toolNames)
			if err != nil {
				return err
			}

			var chat *sgptpb.Chat
			switch {
			case opts.Chat != "":
				chat, err = chatStore.GetChat(ctx, opts.Chat)
				cobra.CheckErr(err)
			case opts.Continue:
				chat, err = chatStore.LatestChat(ctx)
				cobra.CheckErr(err)
				opts.Chat = chat.Name
			default:
				chat = &sgptpb.Chat{
					Files: filePaths,
					Tags:  tags,
					Metadata: &sgptpb.ChatMetadata{
						CurrentModel: selectedModel.Name,
					},
				}
			}

			additionalMessages := make([]*aipb.Message, 0, len(files)+len(toolEngineConfigurations)+1)
			additionalMessages = append(additionalMessages, ai.NewSystemMessage(ai.NewTextBlock(parsedRole.Prompt)))
			for _, toolEngineConfiguration := range toolEngineConfigurations {
				additionalMessages = append(additionalMessages, ai.NewUserMessage(ai.NewTextBlock(toolEngineConfiguration.Instructions)))
			}
			for _, parsedFile := range files {
				additionalMessages = append(additionalMessages, ai.NewUserMessage(ai.NewTextBlock(fmt.Sprintf("file %s: `%s`", parsedFile.Path, parsedFile.Content))))
			}

			params := session.Params{
				Model:              selectedModel,
				Role:               parsedRole,
				MaxTokens:          opts.MaxTokens,
				Temperature:        opts.Temperature,
				Chat:               opts.Chat,
				AdditionalMessages: additionalMessages,
				InjectedFiles:      filePaths,
				Tools:              toolNames,
			}

			app := tui.NewApp(ctx, chatStore, registry, chat, params)
			app.SetAgentSessionFactory(func(ctx context.Context, request *agent.LaunchRequest) (*session.Session, []string, error) {
				model := selectedModel
				if request.Model != "" {
					var err error
					model, err = chatStore.ResolveModel(ctx, request.Model)
					if err != nil {
						return nil, nil, err
					}
				}
				subRegistry, subToolEngineConfigurations, err := buildRegistry(ctx, request.Tools)
				if err != nil {
					return nil, nil, err
				}
				subFiles, err := file.Parse(&file.InjectionOpts{Files: request.Files})
				if err != nil {
					return nil, nil, err
				}
				subFilePaths := make([]string, len(subFiles))
				for i, parsedFile := range subFiles {
					subFilePaths[i] = parsedFile.Path
				}
				// Mirror the CLI-launched chat context assembly exactly.
				subMessages := make([]*aipb.Message, 0, len(subFiles)+len(subToolEngineConfigurations)+1)
				subMessages = append(subMessages, ai.NewSystemMessage(ai.NewTextBlock(parsedRole.Prompt)))
				for _, toolEngineConfiguration := range subToolEngineConfigurations {
					subMessages = append(subMessages, ai.NewUserMessage(ai.NewTextBlock(toolEngineConfiguration.Instructions)))
				}
				for _, parsedFile := range subFiles {
					subMessages = append(subMessages, ai.NewUserMessage(ai.NewTextBlock(fmt.Sprintf("file %s: `%s`", parsedFile.Path, parsedFile.Content))))
				}
				subChat := &sgptpb.Chat{
					Files: subFilePaths,
					Tags:  []string{"agent"},
					Metadata: &sgptpb.ChatMetadata{
						CurrentModel: model.Name,
					},
				}
				subParams := session.Params{
					Model:              model,
					Role:               parsedRole,
					MaxTokens:          opts.MaxTokens,
					Temperature:        opts.Temperature,
					Tools:              request.Tools,
					AdditionalMessages: subMessages,
					InjectedFiles:      subFilePaths,
				}
				return session.New(ctx, chatStore, subRegistry, subChat, subParams), subFilePaths, nil
			})
			agentTool.SetLauncher(app)
			program := tea.NewProgram(app, tea.WithContext(ctx))
			app.SetProgram(program)
			if _, err := program.Run(); err != nil {
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
		models, _ := chatStore.ListModels(cmd.Context(), false)
		return filterModels(models, toComplete), cobra.ShellCompDirectiveNoFileComp
	})

	cmd.RegisterFlagCompletionFunc("tool", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		// Built-in tools complete alongside configured tool engines.
		candidates := tool.BuiltinNames()
		for _, toolEngineConfiguration := range config.GetToolEngines() {
			candidates = append(candidates, toolEngineConfiguration.GetName())
		}
		var names []string
		for _, name := range candidates {
			if toComplete == "" || strings.Contains(strings.ToLower(name), strings.ToLower(toComplete)) {
				names = append(names, name)
			}
		}
		return names, cobra.ShellCompDirectiveNoFileComp
	})

	cmd.RegisterFlagCompletionFunc("role", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		var names []string
		for _, configuredRole := range config.Chat.GetRoles() {
			name := configuredRole.GetName()
			if toComplete == "" || strings.Contains(strings.ToLower(name), strings.ToLower(toComplete)) {
				names = append(names, name)
			}
			if alias := configuredRole.GetAlias(); alias != "" {
				if toComplete == "" || strings.Contains(strings.ToLower(alias), strings.ToLower(toComplete)) {
					names = append(names, alias)
				}
			}
		}
		return names, cobra.ShellCompDirectiveNoFileComp
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
