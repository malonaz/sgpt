package chat

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/user"
	"path"
	"runtime"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/pkg/errors"
	openai "github.com/sashabaranov/go-openai"
	"github.com/spf13/cobra"

	"github.com/malonaz/sgpt/configuration"
)

const (
	asciiSeparator       = "----------------------------------------------------------------------------------------------------------------------------------"
	asciiSeparatorInject = "-----------------------------------------------------%s [%s]------------------------------------------------------\n"
	temporaryChat        = "Single-use Chat"
)

// NewCmd instantiates and returns the inventory flow Cmd.
func NewCmd(openAIClient *openai.Client, config *configuration.Config) *cobra.Command {
	// Initialize chat directory.
	err := initializeChatDirectoryIfNotExist(config.ChatDirectory)
	cobra.CheckErr(err)

	var opts struct {
		ChatID         string
		Files          []string
		Model          string
		FileExtensions []string
		Code           bool
		NoRole         bool
	}
	cmd := &cobra.Command{
		Use:   "chat",
		Short: "Back and forth chat. Available models: (gpt-3.5-turbo, gpt-4)",
		Long:  "Back and forth chat",
		Args:  cobra.ExactArgs(0),
		Run: func(cmd *cobra.Command, args []string) {
			// Parse a chat if relevant.
			chat := &Chat{}
			if opts.ChatID != "" {
				chat, err = parseChat(config.ChatDirectory, opts.ChatID)
				cobra.CheckErr(err)
			} else {
				opts.ChatID = temporaryChat
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

			// Inject any files...
			additionalMessages := make([]openai.ChatCompletionMessage, 0, len(opts.Files))
			injectFile := func(filepath string) {
				// Apply filter
				if !hasValidExtension(filepath, opts.FileExtensions) {
					return
				}

				bytes, err := os.ReadFile(filepath)
				cobra.CheckErr(err)
				content := string(bytes)
				// convert CRLF to LF
				strings.Replace(content, "\n", "", -1)
				message := openai.ChatCompletionMessage{
					Role:    openai.ChatMessageRoleUser,
					Content: fmt.Sprintf("file %d: `%s`", len(additionalMessages)+1, content),
				}
				additionalMessages = append(additionalMessages, message)
				fileColor.Printf("injecting file #%d: %s\n", len(additionalMessages), filepath)
			}
			for _, file := range opts.Files {
				injectIntoContext(file, injectFile)
			}

			if opts.NoRole {
				if opts.Code {
					message := openai.ChatCompletionMessage{
						Role:    openai.ChatMessageRoleUser,
						Content: codePrompt,
					}
					additionalMessages = append(additionalMessages, message)
				} else {
					// Inject code mode.
					// Get some variable information to template prompts.
					os := runtime.GOOS
					user, err := user.Current()
					cobra.CheckErr(err)
					message := openai.ChatCompletionMessage{
						Role:    openai.ChatMessageRoleUser,
						Content: fmt.Sprintf(defaultPrompt, os, user),
					}
					additionalMessages = append(additionalMessages, message)
				}
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

				if opts.ChatID != temporaryChat { // Do not save temporary chats.
					err = chat.Save(config.ChatDirectory, opts.ChatID)
					cobra.CheckErr(err)
				}
			}
		},
	}
	cmd.Flags().StringVar(&opts.Model, "model", "", "override default Open AI model")
	cmd.Flags().StringVar(&opts.ChatID, "id", "", "specify a chat id. Defaults to latest one")
	cmd.Flags().StringSliceVar(&opts.Files, "file", nil, "specify file content to inject into the context")
	cmd.Flags().StringSliceVar(&opts.FileExtensions, "ext", nil, "specify file extensions to accept")
	cmd.Flags().BoolVar(&opts.Code, "code", false, "if true, prints code")
	cmd.Flags().BoolVar(&opts.NoRole, "no_role", false, "if true, does not inject a role into the context")
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

func injectIntoContext(filepath string, injectFN func(filepath string)) {
	// Expand the path to escape `~`.
	filepath, err := configuration.ExpandPath(filepath)
	cobra.CheckErr(err)
	// Here we remove the "/..." if there is one, and record whether it existed.
	filepath, recurse := strings.CutSuffix(filepath, "/...")

	// Check whether `filepath` is a directory.
	fileInfo, err := os.Stat(filepath)
	cobra.CheckErr(err)
	if !fileInfo.IsDir() {
		if recurse {
			cobra.CheckErr(errors.Errorf("cannot recurse on a file: %s", filepath))
		}
		injectFN(filepath)
		return
	}

	// It is a directory
	directory := filepath
	dirEntries, err := os.ReadDir(directory)
	cobra.CheckErr(err)
	for _, dirEntry := range dirEntries {
		dirEntryInfo, err := dirEntry.Info()
		cobra.CheckErr(err)

		if dirEntry.IsDir() {
			if recurse {
				injectIntoContext(path.Join(directory, dirEntryInfo.Name())+"/...", injectFN)
			}
			continue
		}
		injectFN(path.Join(directory, dirEntryInfo.Name()))
	}
}

func hasValidExtension(filename string, validExtensions []string) bool {
	if len(validExtensions) == 0 {
		return true
	}
	for _, validExtension := range validExtensions {
		if strings.HasSuffix(filename, validExtension) {
			return true
		}
	}
	return false
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
