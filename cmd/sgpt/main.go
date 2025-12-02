package main

import (
	"context"
	"fmt"
	"os"

	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	"github.com/malonaz/core/go/grpc"
	"github.com/malonaz/core/go/logging"
	"github.com/spf13/cobra"

	"github.com/malonaz/sgpt/cli/admin"
	"github.com/malonaz/sgpt/cli/chat"
	"github.com/malonaz/sgpt/internal/configuration"
	"github.com/malonaz/sgpt/server"
	"github.com/malonaz/sgpt/store"
)

const defaultConfigFilepath = "~/.config/sgpt/config.v2.json"

func main() {
	if err := run(); err != nil {
		panic(err)
	}
}

func run() error {
	errorLogger, err := logging.NewLogger(&logging.Opts{
		Format: "pretty",
		Level:  "error",
	})
	if err != nil {
		return err
	}

	var configFilepath string

	rootCmd := &cobra.Command{
		Use:     "sgpt",
		Short:   "A CLI for GPT operations",
		Version: "1.0",
		FParseErrWhitelist: cobra.FParseErrWhitelist{
			UnknownFlags: true,
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	rootCmd.PersistentFlags().StringVar(&configFilepath, "config", defaultConfigFilepath, "Path to configuration file")

	// Parse flags early to get config path
	if err := rootCmd.ParseFlags(os.Args); err != nil {
		return fmt.Errorf("parsing flags: %v", err)
	}

	ctx := context.Background()
	config, err := configuration.Parse(configFilepath)
	if err != nil {
		return fmt.Errorf("parsing config: %v", err)
	}

	// Create store
	store, err := store.New(config.Database)
	if err != nil {
		return fmt.Errorf("creating new store: %v", err)
	}
	// Ensure store is closed when the program exits normally
	defer store.Close()

	// Create connection options
	opts := &grpc.Opts{
		SocketPath: "/tmp/core.socket",
		DisableTLS: true,
	}

	// Create gRPC connection
	conn, err := grpc.NewConnection(opts, nil, nil)
	if err != nil {
		return fmt.Errorf("creating connection: %w", err)
	}
	conn.WithLogger(errorLogger)

	if err := conn.Connect(ctx); err != nil {
		return fmt.Errorf("connecting: %w", err)
	}
	defer conn.Close()
	aiClient := aiservicepb.NewAiClient(conn.Get())

	rootCmd.AddCommand(server.NewServeCmd(store))
	rootCmd.AddCommand(chat.NewCmd(config, store, aiClient))
	rootCmd.AddCommand(chat.NewGenerateChatTitlesCmd(config, store, aiClient))
	rootCmd.AddCommand(admin.NewListModelsCmd(aiClient))
	return rootCmd.Execute()
}
