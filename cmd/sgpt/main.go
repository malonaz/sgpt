package main

import (
	"github.com/spf13/cobra"

	"github.com/malonaz/sgpt/chat"
	"github.com/malonaz/sgpt/diff"
	"github.com/malonaz/sgpt/embed"
	"github.com/malonaz/sgpt/internal/configuration"
)

const configFilepath = "~/.config/sgpt/config.json"

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
	rootCmd.AddCommand(chat.NewCmd(config))
	rootCmd.AddCommand(diff.NewCmd(config))
	rootCmd.AddCommand(embed.NewCmd(config))
	rootCmd.Execute()
}
