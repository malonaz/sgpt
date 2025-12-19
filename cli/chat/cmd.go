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
	"github.com/malonaz/core/go/aip"
	"github.com/malonaz/core/go/grpc/interceptor"
	"github.com/spf13/cobra"

	"github.com/malonaz/sgpt/cli/chat/session"
	"github.com/malonaz/sgpt/cli/chat/types"
	"github.com/malonaz/sgpt/internal/configuration"
	"github.com/malonaz/sgpt/internal/file"
	"github.com/malonaz/sgpt/internal/role"
	"github.com/malonaz/sgpt/store"
)

var modelNames []string

// NewCmd instantiates and returns the chat command.
func NewCmd(config *configuration.Config, s *store.Store, aiClient aiservicepb.AiClient) *cobra.Command {
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

			// Build additional messages (files + role)
			additionalMessages := make([]*aipb.Message, 0, len(files)+1)

			// Inject files
			for _, f := range files {
				message := &aipb.Message{
					Role:    aipb.Role_ROLE_USER,
					Content: fmt.Sprintf("file %s: `%s`", f.Path, f.Content),
				}
				additionalMessages = append(additionalMessages, message)
			}

			// Inject role
			if parsedRole != nil {
				message := &aipb.Message{
					Role:    aipb.Role_ROLE_SYSTEM,
					Content: parsedRole.Prompt,
				}
				additionalMessages = append(additionalMessages, message)
			}

			// Create chat options
			chatOpts := types.ChatOptions{
				Model:           opts.Model,
				Role:            parsedRole,
				MaxTokens:       opts.MaxTokens,
				Temperature:     opts.Temperature,
				ReasoningEffort: reasoningEffort,
				EnableTools:     opts.EnableTools,
				ChatID:          opts.ChatID,
			}

			// Create the model
			m, err := session.New(ctx, config, s, aiClient, chat, chatOpts, additionalMessages, filePaths)
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
		if len(modelNames) == 0 {
			err := getModelNames(cmd.Context(), aiClient)
			cobra.CheckErr(err)
		}
		return filterModels(modelNames, toComplete), cobra.ShellCompDirectiveNoFileComp
	})

	return cmd
}

func filterModels(models []string, prefix string) []string {
	if prefix == "" {
		return models
	}

	var matches []string
	lowerPrefix := strings.ToLower(prefix)

	for _, model := range models {
		if strings.Contains(strings.ToLower(model), lowerPrefix) {
			matches = append(matches, model)
		}
	}

	return matches
}

func getModelNames(ctx context.Context, aiClient aiservicepb.AiClient) error {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	ctx = interceptor.WithFieldMask(ctx, "next_page_token,models.name")

	listModelsRequest := &aiservicepb.ListModelsRequest{
		Parent: "providers/-",
	}

	models, err := aip.Paginate[*aipb.Model](ctx, listModelsRequest, aiClient.ListModels)
	if err != nil {
		return err
	}
	for _, model := range models {
		modelNames = append(modelNames, model.Name)
	}
	return nil
}
