package cache

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/malonaz/sgpt/internal/cache"
)

func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cache",
		Short: "Manage the SGPT cache",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "clear",
		Short: "Clear all cached data",
		RunE: func(cmd *cobra.Command, args []string) error {
			cacheDir := cache.Dir()
			if err := os.RemoveAll(cacheDir); err != nil {
				return fmt.Errorf("clearing cache: %w", err)
			}
			fmt.Println("Cache cleared.")
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "path",
		Short: "Print the cache directory path",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(cache.Dir())
		},
	})

	return cmd
}
