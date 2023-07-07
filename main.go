package main

import (
	openai "github.com/sashabaranov/go-openai"
	"github.com/spf13/cobra"

	"github.com/malonaz/sgpt/chat"
	"github.com/malonaz/sgpt/configuration"
)

const configFilepath = "~/.sgpt/config.json"

var rootCmd = &cobra.Command{
	Use:   "sgpt",
	Short: "A CLI for GPT operations",
}

func main() {
	config, err := configuration.Parse(configFilepath)
	if err != nil {
		panic(err)
	}

	// Instantiate open api client.
	client := openai.NewClient(config.OpenaiAPIKey)

	// Instantiate an openAI client.
	rootCmd.AddCommand(chat.NewCmd(client, config))
	rootCmd.Execute()
}
