package main

import (
	openai "github.com/sashabaranov/go-openai"
	"github.com/spf13/cobra"

	"github.com/malonaz/sgpt/chat"
	"github.com/malonaz/sgpt/diff"
	"github.com/malonaz/sgpt/embed"
	"github.com/malonaz/sgpt/internal/configuration"
)

const configFilepath = "~/.sgpt/internal/config.json"

var rootCmd = &cobra.Command{
	Use:     "sgpt",
	Short:   "A CLI for GPT operations",
	Version: "1.0",
}

func main() {
	config, err := configuration.Parse(configFilepath)
	if err != nil {
		panic(err)
	}

	// Instantiate open api client.
	client := openai.NewClient(config.OpenaiAPIKey)

	rootCmd.AddCommand(chat.NewCmd(client, config))
	rootCmd.AddCommand(diff.NewCmd(client, config))
	rootCmd.AddCommand(embed.NewCmd(client, config))
	rootCmd.Execute()
}
