/*
Copyright © 2024 Victor Hang
*/
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/Banh-Canh/jtui/internal/ui"
	"github.com/Banh-Canh/jtui/pkg/jellyfin"
)

var queryCmd = &cobra.Command{
	Use:   "browse",
	Short: "Browse Jellyfin media library",
	Long: `
Browse your Jellyfin media library using an interactive TUI.

Navigate through libraries, folders, and media files.
Use arrow keys or hjkl to navigate, Enter to open, Space/p to play.`,
	Run: func(cmd *cobra.Command, args []string) {
		// Create and authenticate client
		client, err := jellyfin.ConnectFromConfig(func(key string) string {
			return viper.GetString(key)
		})
		if err != nil {
			fmt.Printf("❌ Error connecting to Jellyfin: %v\n", err)
			os.Exit(1)
		}

		// Start TUI with authenticated client
		ui.MenuWithClient(client)
	},
}

func init() {
	RootCmd.AddCommand(queryCmd)
}
