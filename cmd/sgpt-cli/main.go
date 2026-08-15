package main

import (
	"context"
	"fmt"
	"os"

	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	"github.com/malonaz/core/go/grpc"
	"github.com/malonaz/core/go/logging"
	"github.com/spf13/cobra"

	"github.com/malonaz/sgpt/cli/cache"
	"github.com/malonaz/sgpt/cli/chat"
	"github.com/malonaz/sgpt/internal/configuration"
)

const defaultConfigFilepath = "~/.config/sgpt/.sgpt.json"

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

	if err := rootCmd.ParseFlags(os.Args); err != nil {
		return fmt.Errorf("parsing flags: %v", err)
	}

	ctx := context.Background()
	config, err := configuration.Parse(configFilepath)
	if err != nil {
		return fmt.Errorf("parsing config: %v", err)
	}

	clientNameToGRPCConnection := map[string]*grpc.Connection{}
	for _, grpcClient := range config.GetGrpcClients() {
		opts, err := grpc.ParseOpts(grpcClient.BaseUrl)
		if err != nil {
			return fmt.Errorf("parsing base URL: %w", err)
		}
		conn, err := grpc.NewConnection(opts, nil, nil)
		if err != nil {
			return fmt.Errorf("creating connection: %w", err)
		}
		conn.WithLogger(errorLogger)
		conn.WithMetadata(grpcClient.ApiKeyHeader, grpcClient.ApiKey)
		if err := conn.Connect(ctx); err != nil {
			return fmt.Errorf("connecting: %w", err)
		}
		defer conn.Close()
		clientNameToGRPCConnection[grpcClient.GetName()] = conn
	}

	aiClient := aiservicepb.NewAiServiceClient(clientNameToGRPCConnection[config.GetAiService()].Get())

	rootCmd.AddCommand(chat.NewCmd(config, aiClient, clientNameToGRPCConnection))
	rootCmd.AddCommand(cache.NewCmd())
	return rootCmd.Execute()
}
