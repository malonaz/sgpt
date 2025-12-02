package admin

import (
	"fmt"

	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	"github.com/malonaz/core/go/grpc/interceptor"
	"github.com/malonaz/core/go/pbutil"
	"github.com/spf13/cobra"

	"github.com/malonaz/sgpt/internal/cli"
)

// NewListModelsCmd instantiates and returns the list-models command.
func NewListModelsCmd(aiClient aiservicepb.AiClient) *cobra.Command {
	var opts struct {
		Provider string
		Verbose  bool
	}

	cmd := &cobra.Command{
		Use:   "list-models",
		Short: "List available AI models",
		Long:  "List all available AI models from the specified provider",
		Run: func(cmd *cobra.Command, args []string) {
			ctx := cmd.Context()
			if !opts.Verbose {
				ctx = interceptor.WithFieldMask(ctx, "models.name,models.ttt")
			}

			// Build the parent resource name
			parent := fmt.Sprintf("providers/%s", opts.Provider)

			// Track pagination state
			pageToken := ""
			pageNumber := 1

			for {
				// Create the request
				request := &aiservicepb.ListModelsRequest{
					Parent:    parent,
					PageToken: pageToken,
					PageSize:  5,
				}

				// Call the API
				response, err := aiClient.ListModels(ctx, request)
				cobra.CheckErr(err)

				// Display results
				cli.Title("Available Models - Page %d (%d models)", pageNumber, len(response.Models))
				fmt.Println()

				bytes, err := pbutil.MarshalPretty(response)
				cobra.CheckErr(err)
				fmt.Println(string(bytes))
				fmt.Println()

				// Check if there are more pages
				if response.NextPageToken == "" {
					cli.Separator()
					cli.Title("End of Results")
					break
				}

				// Ask user if they want to continue
				cli.Separator()
				if !cli.QueryUserDefaultYes("Load next page?") {
					break
				}

				// Update pagination state
				pageToken = response.NextPageToken
				pageNumber++
				fmt.Println()
			}
		},
	}

	cmd.Flags().StringVarP(&opts.Provider, "provider", "p", "-", "Provider name (use '-' for all providers)")
	cmd.Flags().BoolVar(&opts.Verbose, "verbose", false, "Show all model fields.")

	return cmd
}
