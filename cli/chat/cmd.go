package chat

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/grpc"
	"github.com/spf13/cobra"

	"github.com/malonaz/sgpt/cli/tui"
	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/configuration"
	"github.com/malonaz/sgpt/internal/debug"
	"github.com/malonaz/sgpt/internal/file"
	gograph "github.com/malonaz/sgpt/internal/graph"
	goignore "github.com/malonaz/sgpt/internal/ignore"
	"github.com/malonaz/sgpt/internal/lore"
	"github.com/malonaz/sgpt/internal/repo"
	"github.com/malonaz/sgpt/internal/role"
	"github.com/malonaz/sgpt/internal/session"
	"github.com/malonaz/sgpt/internal/store"
	"github.com/malonaz/sgpt/internal/tool"
	"github.com/malonaz/sgpt/internal/tool/agent"
	"github.com/malonaz/sgpt/internal/tool/diff"
	toolio "github.com/malonaz/sgpt/internal/tool/io"
	"github.com/malonaz/sgpt/internal/tool/lores"
	"github.com/malonaz/sgpt/internal/tool/rpc"
	"github.com/malonaz/sgpt/internal/tool/shell"
)

func NewCmd(
	config *sgptpb.Configuration,
	aiClient aiservicepb.AiServiceClient,
	clientNameToGRPCConnection map[string]*grpc.Connection,
) *cobra.Command {
	chatStore := store.New(config, aiClient)

	// One index of the configuration's imports, shared by every kind of
	// imported artifact: roles, tool sets and lores are all addressed
	// "@{import}//...".
	imports := repo.NewImports(config.GetImports())
	// Empty outside a repo: chat still works, just without artifacts.
	repoRoot, _ := gograph.FindRoot(".")

	// Discover the enclosing repo's .sgpt artifacts (roles, tool sets)
	// plus imports.
	buildForest := func() (*gograph.Forest, error) {
		if repoRoot == "" {
			return gograph.NewForest(&gograph.Tree{PathToDir: map[string]*gograph.Dir{}}, imports, configuration.LoadIgnore), nil
		}
		tree, err := gograph.Scan(repoRoot, config.GetIgnore())
		if err != nil {
			return nil, err
		}
		return gograph.NewForest(tree, imports, configuration.LoadIgnore), nil
	}
	forest, forestErr := buildForest()

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

	loreIndex := lore.NewIndex(repoRoot, imports)
	searchLoresTool := &lores.Tool{Index: loreIndex}

	cmd := &cobra.Command{
		Use: "chat",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 365*24*time.Hour)
			defer cancel()
			// File discovery (the picker) honors the configuration's ignore
			// patterns plus .gitignore files, rooted at the cwd.
			if cwd, err := os.Getwd(); err == nil {
				matcher := goignore.NewMatcher(cwd, config.GetIgnore())
				file.SetDiscoverFilter(func(path string, isDirectory bool) bool {
					relativePath, err := filepath.Rel(cwd, path)
					if err != nil || strings.HasPrefix(relativePath, "..") {
						return false
					}
					if isDirectory {
						// Parse the directory's .gitignore before its children
						// are judged (walk order guarantees parent-first).
						matcher.LoadDirectory(relativePath)
					}
					return matcher.Ignored(relativePath, isDirectory)
				})
			}
			if opts.Debug {
				if _, err := debug.Init(ctx); err != nil {
					return fmt.Errorf("starting debug server: %w", err)
				}
			}

			// Discoverable tool engines warrant the discovery-protocol
			// section of the system prompt.
			opts.Role.ToolDiscovery = forestErr == nil && len(forest.ToolSets()) > 0
			parsedRole, err := opts.Role.Parse()
			// A failed graph discovery empties the role registry, making the
			// resulting "unknown role" misleading — surface the real cause.
			if err != nil && forestErr != nil {
				err = fmt.Errorf("%v — likely because graph discovery failed: %w", err, forestErr)
			}
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
			var filePaths []string
			files, err := file.Parse(opts.FileInjection)
			cobra.CheckErr(err)
			// Role files are curated in config: --ext is a convenience for
			// ad-hoc directory injection and must never drop them.
			roleFiles, err := file.Parse(&file.InjectionOpts{Files: parsedRole.GetFiles()})
			cobra.CheckErr(err)
			// Lores configured as always-on context enter as plain files:
			// selectors are resolved to paths so injection, dedupe and the
			// file picker treat them like any other file.
			var defaultLorePaths []string
			for _, selector := range config.Chat.GetDefaultLores() {
				_, path, err := loreIndex.Resolve(selector)
				cobra.CheckErr(err)
				defaultLorePaths = append(defaultLorePaths, path)
			}
			loreFiles, err := file.Parse(&file.InjectionOpts{Files: defaultLorePaths})
			cobra.CheckErr(err)
			realFilePaths := make([]string, 0, len(files)+len(roleFiles)+len(loreFiles))
			for _, parsedFile := range append(append(files, roleFiles...), loreFiles...) {
				realFilePaths = append(realFilePaths, parsedFile.Path)
			}
			filePaths = append(filePaths, file.Normalize(realFilePaths)...)

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
			// search_lores searches every reachable library.
			registry.Register(tool.HandlerIDSearchLores, searchLoresTool)

			availableToolNames := tool.BuiltinNames()
			for _, name := range tool.BuiltinNames() {
				builtinTool, _ := tool.Builtin(name)
				registry.AddTools(builtinTool)
			}
			// Engines are listed (picker candidates) but NOT dialed here:
			// initialization happens on first enablement, via resolveTool.
			toolSetConfigurations := forest.ToolSets()
			toolEngineManager := rpc.NewManager(config, clientNameToGRPCConnection, toolSetConfigurations)
			registry.Register(tool.HandlerIDEngine, toolEngineManager)
			for _, toolSetConfiguration := range toolSetConfigurations {
				availableToolNames = append(availableToolNames, toolSetConfiguration.GetName())
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

			toolNames := append(opts.Tools, parsedRole.GetTools()...)
			toolNames = append(toolNames, config.Chat.GetDefaultTools()...)
			// The same tool may arrive via --tool, the role and the config.
			toolNames = dedupe(toolNames)
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
				// Chats are created eagerly: sessions need the resource name
				// to persist messages and titles.
				newChat := &aipb.Chat{}
				store.SetTags(newChat, tags)
				store.SetFiles(newChat, filePaths)
				store.SetCurrentModel(newChat, selectedModel.Name)
				chat, err = chatStore.CreateChat(ctx, newChat)
				cobra.CheckErr(err)
				opts.Chat = chat.Name
			}

			// Messages are server-side resources: load the history to seed
			// the session (empty for a fresh chat).
			messages, err := chatStore.ListMessages(ctx, chat.Name)
			cobra.CheckErr(err)

			params := session.Params{
				Model:              selectedModel,
				Role:               parsedRole,
				MaxTokens:          opts.MaxTokens,
				Temperature:        opts.Temperature,
				Chat:               opts.Chat,
				SystemPrompt:       parsedRole.Prompt,
				InjectedFiles:      filePaths,
				Tools:              toolNames,
				AvailableToolNames: availableToolNames,
				ResolveTool:        resolveTool,
				LoreNameForPath:    loreIndex.NameForPath,
			}

			app := tui.NewApp(ctx, chatStore, registry, chat, messages, params)
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
				subChat := &aipb.Chat{}
				// Agent-provided title: skips auto-generation and labels the tab.
				subChat.Title = request.Title
				store.SetTags(subChat, []string{"agent"})
				store.SetFiles(subChat, subFilePaths)
				store.SetCurrentModel(subChat, model.Name)
				store.SetParentChatID(subChat, chat.Name)
				subChat, err = chatStore.CreateChat(ctx, subChat)
				if err != nil {
					return nil, nil, err
				}
				subParams := session.Params{
					Model:              model,
					Role:               parsedRole,
					MaxTokens:          opts.MaxTokens,
					Temperature:        opts.Temperature,
					Tools:              request.Tools,
					AvailableToolNames: availableToolNames,
					ResolveTool:        resolveTool,
					Chat:               subChat.Name,
					SystemPrompt:       parsedRole.Prompt,
					InjectedFiles:      subFilePaths,
					LoreNameForPath:    loreIndex.NameForPath,
				}
				return session.New(ctx, chatStore, registry, subChat, nil, subParams), subFilePaths, nil
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
	opts.Role = role.GetOpts(cmd, config.Chat.GetDefaultRole(), forest.Roles())
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
		// Built-in tools complete alongside discovered tool engines;
		// imports are only offered once the user types "@".
		candidates := tool.BuiltinNames()
		toolSetConfigurations := forest.PrimaryToolSets()
		if strings.HasPrefix(toComplete, "@") {
			toolSetConfigurations = forest.ToolSets()
		}
		for _, toolSetConfiguration := range toolSetConfigurations {
			candidates = append(candidates, toolSetConfiguration.GetName())
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
		// Imports are only offered once the user types "@".
		completableRoles := forest.PrimaryRoles()
		if strings.HasPrefix(toComplete, "@") {
			completableRoles = forest.Roles()
		}
		var names []string
		for _, configuredRole := range completableRoles {
			name := configuredRole.GetName()
			if toComplete == "" || strings.Contains(strings.ToLower(name), strings.ToLower(toComplete)) {
				names = append(names, name)
			}
			// Aliases are accepted as input but never offered: completion
			// always shows the canonical selector.
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

func dedupe(values []string) []string {
	valueSet := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if valueSet[value] {
			continue
		}
		valueSet[value] = true
		result = append(result, value)
	}
	return result
}
