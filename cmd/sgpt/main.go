package main

import (
	"github.com/spf13/cobra"

	"github.com/malonaz/sgpt/chat"
	"github.com/malonaz/sgpt/diff"
	"github.com/malonaz/sgpt/embed"
	"github.com/malonaz/sgpt/internal/configuration"
	"github.com/malonaz/sgpt/server"
	"github.com/malonaz/sgpt/store"
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

	// Create store
	store, err := store.New(config.Database)
	if err != nil {
		panic(err)
	}
	// Ensure store is closed when the program exits normally
	defer store.Close()

	rootCmd.AddCommand(server.NewServeCmd(store))
	rootCmd.AddCommand(chat.NewCmd(config, store))
	rootCmd.AddCommand(chat.NewListChatsCmd(config, store))
	rootCmd.AddCommand(diff.NewCmd(config))
	rootCmd.AddCommand(embed.NewCmd(config))
	rootCmd.Execute()
}
