package diff

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/AlecAivazis/survey/v2"
	"github.com/fatih/color"
	"github.com/pkg/errors"
	"github.com/sashabaranov/go-openai"
	"github.com/spf13/cobra"

	"github.com/malonaz/sgpt/configuration"
	"github.com/malonaz/sgpt/model"
)

const prompt = `IMPORTANT: Provide only plain text without Markdown formatting.
IMPORTANT: Do not include markdown formatting such as ` + "```" + `.
Output a git commit messages for the provided diff using the following format:
Short (72 chars or less) summary

- Bullet points are okay, too.
- Typically a hyphen or asterisk is used for the bullet, followed by a
  single space. Use a hanging indent.
`

const (
	asciiSeparator       = "----------------------------------------------------------------------------------------------------------------------------------"
	asciiSeparatorInject = "--------------------------------------------------------------Diff----------------------------------------------------------------"
)

// NewCmd instantiates and returns the diff command.
func NewCmd(openAIClient *openai.Client, config *configuration.Config) *cobra.Command {
	var opts struct {
		Model *model.Opts
	}

	cmd := &cobra.Command{
		Use:   "diff",
		Short: "Generate diff commit message",
		Long:  "Generate diff commit message",
		Args:  cobra.ExactArgs(0),
		Run: func(cmd *cobra.Command, args []string) {
			// Run git diff.
			path, err := exec.LookPath("git")
			cobra.CheckErr(errors.Wrapf(err, "git not found in your PATH"))
			gitDiffCommand := exec.Command(path, "diff", "--cached")
			bytesBuffer := &bytes.Buffer{}
			gitDiffCommand.Stdout = bytesBuffer
			err = gitDiffCommand.Run()
			cobra.CheckErr(err)
			gitDiff := bytesBuffer.String()

			// Colors.
			fileColor := color.New(color.FgRed)
			aiColor := color.New(color.FgCyan)
			formatColor := color.New(color.FgGreen)
			costColor := color.New(color.FgYellow)
			// Print title.
			formatColor.Println(asciiSeparator)
			formatColor.Println(asciiSeparatorInject)
			formatColor.Println(asciiSeparator)

			// Remove from the diff files we don't care about:
			fileColor.Printf("Ignoring the following files:\n -%s\n", strings.Join(config.DiffIgnoreFiles, "\n -"))
			formatColor.Println(asciiSeparator)
			parts := strings.Split(gitDiff, "diff --git")
			filteredParts := []string{}
			for _, part := range parts {
				ignore := false
				for _, ignoreFile := range config.DiffIgnoreFiles {
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

			// Inject the diff into the context.
			messages := []openai.ChatCompletionMessage{}
			message := openai.ChatCompletionMessage{
				Role:    openai.ChatMessageRoleSystem,
				Content: fmt.Sprintf("consider this git diff: `%s`", filteredGitDiff),
			}
			messages = append(messages, message)

			// Query message.
			message = openai.ChatCompletionMessage{
				Role:    openai.ChatMessageRoleUser,
				Content: prompt,
			}
			messages = append(messages, message)

			// Set the model.
			model, err := model.Parse(opts.Model, config)
			cobra.CheckErr(err)

			// Open stream.
			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(config.RequestTimeout)*time.Second)
			defer cancel()
			request := openai.ChatCompletionRequest{
				Model:    model.ID,
				Messages: messages,
				Stream:   true,
			}
			requestTokens, requestCost, err := model.CalculateRequestCost(messages...)
			cobra.CheckErr(err)
			costColor.Printf("Request contains %d tokens costing $%s\n", requestTokens, requestCost.String())
			formatColor.Println(asciiSeparator)

			stream, err := openAIClient.CreateChatCompletionStream(ctx, request)
			cobra.CheckErr(err)
			defer stream.Close()

			// Consume stream.
			chatCompletionMessage := openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant}
			for {
				response, err := stream.Recv()
				if errors.Is(err, io.EOF) {
					break
				}
				cobra.CheckErr(err)
				aiColor.Printf(response.Choices[0].Delta.Content)
				chatCompletionMessage.Content += response.Choices[0].Delta.Content
			}
			fmt.Printf("\n")
			responseTokens, responseCost, err := model.CalculateResponseCost(chatCompletionMessage)
			cobra.CheckErr(err)
			formatColor.Println(asciiSeparator)
			costColor.Printf("Response contains %d tokens costing $%s\n", responseTokens, responseCost.String())
			costColor.Printf("Total cost is $%s\n", requestCost.Add(responseCost).String())
			formatColor.Println(asciiSeparator)

			// Check if user wants to commit the message.
			surveyQuestion := &survey.Confirm{
				Message: "Apply commit",
			}
			confirm := false
			survey.AskOne(surveyQuestion, &confirm)
			if !confirm {
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

	opts.Model = model.GetOpts(cmd, config)
	return cmd
}
