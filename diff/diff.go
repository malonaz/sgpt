package diff

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/sashabaranov/go-openai"
	"github.com/spf13/cobra"
	"github.com/waigani/diffparser"

	"github.com/malonaz/sgpt/internal/cli"
	"github.com/malonaz/sgpt/internal/configuration"
	"github.com/malonaz/sgpt/internal/file"
	"github.com/malonaz/sgpt/internal/llm"
)

// NewCmd instantiates and returns the diff command.
func NewCmd(config *configuration.Config) *cobra.Command {
	var opts struct {
		LLM     *llm.Opts
		Message string
	}
	prompt := strings.ReplaceAll(prompt, "@", "`")
	cmd := &cobra.Command{
		Use:   "diff",
		Short: "Generate diff commit message",
		Long:  "Generate diff commit message",
		Args:  cobra.ExactArgs(0),
		Run: func(cmd *cobra.Command, args []string) {
			if opts.LLM.Model == "" {
				opts.LLM.Model = config.Diff.DefaultModel
			}
			llmClient, model, provider, err := llm.NewClient(config, opts.LLM)
			cobra.CheckErr(err)

			// Headers.
			cli.Title(model.Name)

			// Run git diff.
			path, err := exec.LookPath("git")
			cobra.CheckErr(errors.Wrapf(err, "git not found in your PATH"))
			gitDiffCommand := exec.Command(path, "diff", "--cached")
			bytesBuffer := &bytes.Buffer{}
			gitDiffCommand.Stdout = bytesBuffer
			err = gitDiffCommand.Run()
			cobra.CheckErr(err)
			gitDiff := bytesBuffer.String()

			if len(gitDiff) == 0 {
				cobra.CheckErr(fmt.Errorf("git diff is empty, aborting."))
			}

			// Remove from the diff files we don't care about:
			parts := strings.Split(gitDiff, "diff --git")
			filteredParts := []string{}
			for _, part := range parts {
				ignore := false
				for _, ignoreFile := range config.Diff.IgnoreFiles {
					if strings.Contains(part, ignoreFile) {
						ignore = true
						break
					}
				}
				if !ignore {
					filteredParts = append(filteredParts, part)
				}
			}
			filteredGitDiff := strings.Join(filteredParts, "diff --git")

			// Analyze the diff.
			diff, err := diffparser.Parse(gitDiff)
			cobra.CheckErr(err)
			rootDirToCount := map[string]int{}
			for _, f := range diff.Files {
				filename := f.OrigName
				if f.NewName != filename {
					filename = f.NewName
				}
				if slices.Contains(config.Diff.IgnoreFiles, filename) {
					cli.FileInfo("Ignoring %s\n", filename)
					continue
				}
				// Update the root dir count.
				rootDir := file.GetRootDir(filename)
				rootDirToCount[rootDir] += len(f.Hunks)
			}
			type Scope struct {
				Name  string
				Count int
			}
			scopes := []*Scope{}
			for rootDir, count := range rootDirToCount {
				scope := &Scope{
					Name:  rootDir,
					Count: count,
				}
				scopes = append(scopes, scope)
			}
			sort.Slice(scopes, func(i, j int) bool { return scopes[i].Count > scopes[j].Count })

			messages := []*llm.Message{}
			// Inject diff.
			message := &llm.Message{
				Role:    openai.ChatMessageRoleSystem,
				Content: fmt.Sprintf("[git diff]\n```%s```", filteredGitDiff),
			}
			messages = append(messages, message)

			// Inject diff analysis.
			scopeNames := []string{}
			scopeChanges := []string{}
			for _, scope := range scopes {
				scopeNames = append(scopeNames, scope.Name)
				scopeChanges = append(scopeChanges, fmt.Sprintf("scope [%s] has %d changes.", scope.Name, scope.Count))
			}
			message = &llm.Message{
				Role:    openai.ChatMessageRoleSystem,
				Content: fmt.Sprintf("Scopes available: [%s]\n%s", strings.Join(scopeNames, ", "), strings.Join(scopeChanges, "\n")),
			}
			messages = append(messages, message)

			// Inject the system prompt.
			message = &llm.Message{
				Role:    openai.ChatMessageRoleSystem,
				Content: prompt,
			}
			messages = append(messages, message)

			// Query message.
			message = &llm.Message{
				Role:    openai.ChatMessageRoleUser,
				Content: strings.ReplaceAll(generateGitCommitMessage, "{{message}}", opts.Message),
			}
			messages = append(messages, message)

			// Open stream.
			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(provider.RequestTimeout)*time.Second)
			defer cancel()
			request := &llm.CreateTextGenerationRequest{
				Model:    model.Name,
				Messages: messages,
			}

			stream, err := llmClient.CreateTextGeneration(ctx, request)
			cobra.CheckErr(err)
			defer stream.Close()

			// Consume stream.
			chatCompletionMessage := &llm.Message{Role: openai.ChatMessageRoleAssistant}
			for {
				event, err := stream.Recv()
				if errors.Is(err, io.EOF) {
					break
				}
				cobra.CheckErr(err)
				cli.AIOutput(event.Token)
				chatCompletionMessage.Content += event.Token
			}
			cli.AIOutput("\n")

			// Check if user wants to commit the message.
			if !cli.QueryUser("Apply commit") {
				return
			}

			// Write the commit to a file.
			commitFilepath := "/tmp/sgpt.commit"
			err = os.WriteFile(commitFilepath, []byte(chatCompletionMessage.Content), 0644)
			cobra.CheckErr(err)
			gitCommitCommand := exec.Command(path, "commit", "--file", commitFilepath)
			err = gitCommitCommand.Run()
			cobra.CheckErr(err)
		},
	}

	opts.LLM = llm.GetOpts(cmd)
	cmd.Flags().StringVar(&opts.Message, "message", "", "specify a message to spgt diff")
	return cmd
}
