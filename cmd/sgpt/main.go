package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	"github.com/malonaz/core/go/authentication"
	"github.com/malonaz/core/go/grpc"
	"github.com/malonaz/core/go/logging"
	"github.com/spf13/cobra"

	"github.com/malonaz/sgpt/cli/chat"
	chatservicepb "github.com/malonaz/sgpt/genproto/chat/chat_service/v1"
	"github.com/malonaz/sgpt/internal/configuration"
	"github.com/malonaz/sgpt/store"
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

	ctx = authentication.WithAPIKey(ctx, "x-api-key", config.APIKey)
	rootCmd.SetContext(ctx)

	store, err := store.New(config.Database)
	if err != nil {
		return fmt.Errorf("creating new store: %v", err)
	}
	defer store.Close()

	var opts *grpc.Opts
	if local {
		opts = &grpc.Opts{
			SocketPath: "/tmp/core.socket",
			DisableTLS: true,
		}
	} else {
		host, port, err := parseBaseURL(config.BaseURL)
		if err != nil {
			return fmt.Errorf("parsing base URL: %w", err)
		}
		opts = &grpc.Opts{
			Host:       host,
			Port:       port,
			DisableTLS: true,
		}
	}

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
	chatClient := chatservicepb.NewChatClient(conn.Get())

	rootCmd.AddCommand(webserver.NewServeCmd(store))
	rootCmd.AddCommand(chat.NewCmd(config, store, aiClient, chatClient))
	return rootCmd.Execute()
}

func parseBaseURL(baseURL string) (string, int, error) {
	parts := strings.Split(baseURL, ":")
	if len(parts) != 2 {
		return "", 0, fmt.Errorf("invalid format, expected host:port, got %s", baseURL)
	}
	port, err := strconv.Atoi(parts[1])
	if err != nil {
		return "", 0, fmt.Errorf("invalid port: %w", err)
	}
	return parts[0], port, nil
}
