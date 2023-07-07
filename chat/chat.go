package chat

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/google/uuid"
	"github.com/pkg/errors"
	openai "github.com/sashabaranov/go-openai"
	"github.com/spf13/cobra"

	"github.com/malonaz/sgpt/configuration"
	"github.com/malonaz/sgpt/file"
	"github.com/malonaz/sgpt/role"
)

const (
	asciiSeparator       = "----------------------------------------------------------------------------------------------------------------------------------"
	asciiSeparatorInject = "-----------------------------------------------------%s [%s]------------------------------------------------------\n"
)

// NewCmd instantiates and returns the inventory chat cmd.
func NewCmd(openAIClient *openai.Client, config *configuration.Config) *cobra.Command {
	// Initialize chat directory.
	err := initializeChatDirectoryIfNotExist(config.ChatDirectory)
	cobra.CheckErr(err)

	var opts struct {
		FileInjection *file.InjectionOpts
		Role          *role.Opts
		ChatID        string
		Model         string
	}
	cmd := &cobra.Command{
		Use:   "chat",
		Short: "Back and forth chat",
		Long:  "Back and forth chat. Available models: (gpt-3.5-turbo, gpt-4)",
		Args:  cobra.ExactArgs(0),
		Run: func(cmd *cobra.Command, args []string) {
			// Parse a chat if relevant.
			chat := &Chat{}
			if opts.ChatID != "" {
				chat, err = parseChat(config.ChatDirectory, opts.ChatID)
				cobra.CheckErr(err)
			} else {
				opts.ChatID = uuid.New().String()[:8]
			}

			model := config.DefaultModel
			if opts.Model != "" {
				model = opts.Model
			}

			userColor := color.New(color.Bold).Add(color.Underline)
			aiColor := color.New(color.FgCyan)
			formatColor := color.New(color.FgGreen)
			fileColor := color.New(color.FgRed)

			// Chat headers.
			formatColor.Println(asciiSeparator)
			formatColor.Printf(asciiSeparatorInject, opts.ChatID, model)
			formatColor.Println(asciiSeparator)

			// Inject files.
			files, err := file.Parse(opts.FileInjection)
			cobra.CheckErr(err)
			additionalMessages := make([]openai.ChatCompletionMessage, 0, len(files))
			for _, file := range files {
				message := openai.ChatCompletionMessage{
					Role:    openai.ChatMessageRoleSystem,
					Content: fmt.Sprintf("file %s: `%s`", file.Path, file.Content),
				}
				additionalMessages = append(additionalMessages, message)
				fileColor.Printf("injecting file #%d: %s\n", len(additionalMessages), file.Path)
			}

			// Inject role.
			role, err := role.Parse(opts.Role)
			cobra.CheckErr(err)
			if role != nil {
				message := openai.ChatCompletionMessage{
					Role:    openai.ChatMessageRoleSystem,
					Content: role.Description,
				}
				additionalMessages = append(additionalMessages, message)
			}

			// Print history.
			for _, message := range chat.Messages {
				if message.Role == openai.ChatMessageRoleUser {
					userColor.Printf("-> %s\n", message.Content)
				}
				if message.Role == openai.ChatMessageRoleAssistant {
					aiColor.Println(message.Content)
				}
			}

			ctx := context.Background()
			for {
				// Query user for prompt.
				userColor.Print("\n-> ")
				text := promptUser()
				// convert CRLF to LF
				text = strings.Replace(text, "\n", "", -1)
				chat.Messages = append(chat.Messages, openai.ChatCompletionMessage{
					Role:    openai.ChatMessageRoleUser,
					Content: text,
				})

				// Send request to API and stream response.
				ctx, cancel := context.WithTimeout(ctx, time.Duration(config.RequestTimeout)*time.Second)
				defer cancel()
				request := openai.ChatCompletionRequest{
					Model:    model,
					Messages: append(additionalMessages, chat.Messages...),
					Stream:   true,
				}
				stream, err := openAIClient.CreateChatCompletionStream(ctx, request)
				cobra.CheckErr(err)
				defer stream.Close()

				responseContent := ""
				for {
					response, err := stream.Recv()
					if errors.Is(err, io.EOF) {
						break
					}
					cobra.CheckErr(err)
					content := strings.Replace(response.Choices[0].Delta.Content, "\n\n", "\n", -1)
					content = strings.Replace(content, "%", "%%", -1)
					fmt.Printf(content)
					responseContent += response.Choices[0].Delta.Content
				}
				fmt.Printf("\n")

				// Append the response content to our history.
				chat.Messages = append(chat.Messages, openai.ChatCompletionMessage{
					Role:    openai.ChatMessageRoleAssistant,
					Content: responseContent,
				})

				err = chat.Save(config.ChatDirectory, opts.ChatID)
				cobra.CheckErr(err)
			}
		},
	}
	opts.FileInjection = file.GetOpts(cmd)
	opts.Role = role.GetOpts(cmd)
	cmd.Flags().StringVar(&opts.Model, "model", "", "override default Open AI model")
	cmd.Flags().StringVar(&opts.ChatID, "id", "", "specify a chat id. Defaults to latest one")
	cmd.Flags().BoolVar(&opts.Code, "code", false, "if true, prints code")
	return cmd
}

func initializeChatDirectoryIfNotExist(chatDirectory string) error {
	if _, err := os.Stat(chatDirectory); !os.IsNotExist(err) {
		return nil
	}
	if err := os.MkdirAll(chatDirectory, 0755); err != nil {
		return errors.Wrap(err, "creating chat directory")
	}
	return nil
}

// Chat holds a chat struct.
type Chat struct {
	Messages []openai.ChatCompletionMessage
}

func parseChat(chatDirectory, chatID string) (*Chat, error) {
	path := path.Join(chatDirectory, chatID)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return &Chat{}, nil
	}

	bytes, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.Wrap(err, "reading chat file")
	}

	chat := &Chat{}
	if err = json.Unmarshal(bytes, chat); err != nil {
		return nil, errors.Wrap(err, "unmarshaling into chat")
	}
	return chat, nil
}

// Save a chat to disk.
func (c *Chat) Save(chatDirectory, chatID string) error {
	path := path.Join(chatDirectory, chatID)
	bytes, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return errors.Wrap(err, "marshaling chat to JSON")
	}

	if err := os.WriteFile(path, bytes, 0644); err != nil {
		return errors.Wrap(err, "writing chat to file")
	}

	return nil
}

func promptUser() string {
	reader := bufio.NewReader(os.Stdin)
	var contentBuilder strings.Builder
	for {
		char, _, err := reader.ReadRune()
		cobra.CheckErr(err)
		if char == '\x0B' {
			break
		}
		contentBuilder.WriteRune(char)
	}
	return contentBuilder.String()
}
