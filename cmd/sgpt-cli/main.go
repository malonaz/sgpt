package main

import (
	"context"
	"fmt"
	"os"

	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	"github.com/malonaz/core/go/grpc"
	"github.com/malonaz/core/go/logging"
	"github.com/spf13/cobra"

	"github.com/malonaz/sgpt/cli/chat"
	sgptservicepb "github.com/malonaz/sgpt/genproto/sgpt/sgpt_service/v1"
	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/configuration"
	"github.com/malonaz/sgpt/webserver"
)

const defaultConfigFilepath = "~/.config/sgpt/config.json"

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
	var local bool

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
	rootCmd.PersistentFlags().BoolVar(&local, "local", false, "Use local server")

	if err := rootCmd.ParseFlags(os.Args); err != nil {
		return fmt.Errorf("parsing flags: %v", err)
	}

	ctx := context.Background()
	config, err := configuration.Parse(configFilepath)
	if err != nil {
		return fmt.Errorf("parsing config: %v", err)
	}

	baseURLToGRPCConnection := map[string]*grpc.Connection{}
	grpcClients := []*sgptpb.GrpcClient{
		config.GetAiService(),
		config.GetSgptService(),
	}
	for _, toolEngine := range config.GetToolEngines() {
		grpcClients = append(grpcClients, toolEngine.GetEngineService())
	}
	for _, grpcClient := range grpcClients {
		if _, ok := baseURLToGRPCConnection[grpcClient.GetBaseUrl()]; ok {
			continue
		}
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
		baseURLToGRPCConnection[grpcClient.GetBaseUrl()] = conn
	}

	// Instantiate AI Client.
	aiClient := aiservicepb.NewAiServiceClient(baseURLToGRPCConnection[config.GetAiService().GetBaseUrl()].Get())
	sgptClient := sgptservicepb.NewSgptServiceClient(baseURLToGRPCConnection[config.GetSgptService().GetBaseUrl()].Get())

	rootCmd.AddCommand(webserver.NewServeCmd(sgptClient))
	rootCmd.AddCommand(chat.NewCmd(config, aiClient, sgptClient, baseURLToGRPCConnection))
	rootCmd.AddCommand(chat.NewSummarizeCmd(config, aiClient, sgptClient))
	return rootCmd.Execute()
}
