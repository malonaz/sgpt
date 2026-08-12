package chat

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"
	"github.com/malonaz/core/go/grpc"
	"github.com/spf13/cobra"

	"github.com/malonaz/sgpt/cli/tui"
	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/debug"
	"github.com/malonaz/sgpt/internal/file"
	"github.com/malonaz/sgpt/internal/role"
	"github.com/malonaz/sgpt/internal/search"
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
	baseURLToGRPCConnection map[string]*grpc.Connection,
) *cobra.Command {
	chatStore := store.New(config, aiClient)

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

			// Search is disabled for now: bleve's bolt file lock is exclusive,
			// so a second sgpt window would hang forever on startup waiting
			// for it. Flip to true once multi-process access is solved.
			if false {
				// Best-effort search wiring: the menu falls back to substring
				// filtering whenever the index is unavailable.
				if searchPath, err := search.DefaultPath(); err == nil {
					if searchIndex, err := search.Open(searchPath); err == nil {
						chatStore.SetSearchIndex(searchIndex)
						// Incrementally backfill chats modified since last run.
						go chatStore.SyncSearchIndex(ctx)
					}
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
			files, err := file.Parse(opts.FileInjection)
			cobra.CheckErr(err)
			// Role files are curated in config: --ext is a convenience for
			// ad-hoc directory injection and must never drop them.
			roleFiles, err := file.Parse(&file.InjectionOpts{Files: parsedRole.GetFiles()})
			cobra.CheckErr(err)
			filePaths := make([]string, 0, len(files)+len(roleFiles))
			for _, parsedFile := range append(files, roleFiles...) {
				filePaths = append(filePaths, parsedFile.Path)
			}
			filePaths = file.Normalize(filePaths)

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

			// The registry always carries the FULL tool surface — every
			// builtin and every configured tool engine. Which subset is
			// advertised to the model is a per-session selection (seeded
			// from --tool/role, toggleable mid-chat via the tool picker).
			registry := tool.NewRegistry()
			registry.Register(tool.HandlerIDShell, &shell.Tool{})
			registry.Register(tool.HandlerIDReadFiles, &toolio.ReadFilesTool{})
			registry.Register(tool.HandlerIDDiff, &diff.Tool{})
			registry.Register(tool.HandlerIDReplace, &toolio.ReplaceTool{})
			// Same instance everywhere: sub-agents can spawn sub-agents.
			registry.Register(tool.HandlerIDAgent, agentTool)

			availableToolNames := tool.BuiltinNames()
			for _, name := range tool.BuiltinNames() {
				builtinTool, _ := tool.Builtin(name)
				registry.AddTools(builtinTool)
			}
			// Engines are listed (picker candidates) but NOT dialed here:
			// initialization happens on first enablement, via resolveTool.
			toolEngineManager := rpc.NewManager(config, baseURLToGRPCConnection)
			registry.Register(tool.HandlerIDEngine, toolEngineManager)
			for _, toolEngineConfiguration := range config.GetToolEngines() {
				availableToolNames = append(availableToolNames, toolEngineConfiguration.GetName())
			}

			// resolveTool maps a user-facing name to advertised tool/tool-set
			// names, lazily initializing an engine and registering its tool
			// sets on first use. Cached, so repeat toggles are free.
			var resolveMu sync.Mutex
			resolvedToolNames := map[string][]string{}
			resolveTool := func(ctx context.Context, name string) ([]string, error) {
				resolveMu.Lock()
				defer resolveMu.Unlock()
				if names, ok := resolvedToolNames[name]; ok {
					return names, nil
				}
				if _, ok := tool.Builtin(name); ok {
					resolvedToolNames[name] = []string{name}
					return resolvedToolNames[name], nil
				}
				toolSets, err := toolEngineManager.EnsureEngine(ctx, name)
				if err != nil {
					return nil, err
				}
				registry.AddToolSets(toolSets...)
				toolSetNames := make([]string, 0, len(toolSets))
				for _, toolSet := range toolSets {
					toolSetNames = append(toolSetNames, toolSet.GetName())
				}
				resolvedToolNames[name] = toolSetNames
				return toolSetNames, nil
			}
			validateToolNames := func(names []string) error {
				for _, name := range names {
					// Resolving both validates the name and eagerly dials
					// the engines requested at launch.
					if _, err := resolveTool(ctx, name); err != nil {
						return err
					}
				}
				return nil
			}
			// engineInstructions returns the instruction messages of the
			// engines enabled by name.
			engineInstructions := func(names []string) []*aipb.Message {
				enabledNameSet := map[string]struct{}{}
				for _, name := range names {
					enabledNameSet[name] = struct{}{}
				}
				var messages []*aipb.Message
				for _, toolEngineConfiguration := range config.GetToolEngines() {
					if _, ok := enabledNameSet[toolEngineConfiguration.GetName()]; ok && toolEngineConfiguration.GetInstructions() != "" {
						messages = append(messages, ai.NewUserMessage(ai.NewTextBlock(toolEngineConfiguration.GetInstructions())))
					}
				}
				return messages
			}

			toolNames := append(opts.Tools, parsedRole.GetTools()...)
			if err := validateToolNames(toolNames); err != nil {
				return err
			}

			var chat *aipb.Chat
			switch {
			case opts.Chat != "":
				chat, err = chatStore.GetChat(ctx, opts.Chat)
				cobra.CheckErr(err)
			case opts.Continue:
				chat, err = chatStore.LatestChat(ctx)
				cobra.CheckErr(err)
				opts.Chat = chat.Name
			default:
				chat = &aipb.Chat{Metadata: &aipb.ChatMetadata{}}
				store.SetTags(chat, tags)
				store.SetFiles(chat, filePaths)
				store.SetCurrentModel(chat, selectedModel.Name)
			}

			// File contents are NOT baked in here: the session injects them
			// per turn from InjectedFiles, so they stay toggleable mid-chat.
			additionalMessages := []*aipb.Message{ai.NewSystemMessage(ai.NewTextBlock(parsedRole.Prompt))}
			additionalMessages = append(additionalMessages, engineInstructions(toolNames)...)

			params := session.Params{
				Model:              selectedModel,
				Role:               parsedRole,
				MaxTokens:          opts.MaxTokens,
				Temperature:        opts.Temperature,
				Chat:               opts.Chat,
				AdditionalMessages: additionalMessages,
				InjectedFiles:      filePaths,
				Tools:              toolNames,
				AvailableToolNames: availableToolNames,
				ResolveTool:        resolveTool,
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
				if err := validateToolNames(request.Tools); err != nil {
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
				subMessages := []*aipb.Message{ai.NewSystemMessage(ai.NewTextBlock(parsedRole.Prompt))}
				subMessages = append(subMessages, engineInstructions(request.Tools)...)
				subChat := &aipb.Chat{Metadata: &aipb.ChatMetadata{}}
				// Agent-provided title: skips auto-generation and labels the tab.
				subChat.Title = request.Title
				store.SetTags(subChat, []string{"agent"})
				store.SetFiles(subChat, subFilePaths)
				store.SetCurrentModel(subChat, model.Name)
				subParams := session.Params{
					Model:              model,
					Role:               parsedRole,
					MaxTokens:          opts.MaxTokens,
					Temperature:        opts.Temperature,
					Tools:              request.Tools,
					AvailableToolNames: availableToolNames,
					ResolveTool:        resolveTool,
					AdditionalMessages: subMessages,
					InjectedFiles:      subFilePaths,
				}
				return session.New(ctx, chatStore, registry, subChat, subParams), subFilePaths, nil
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
