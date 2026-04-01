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

Navigate with arrow keys or hjkl. Enter to open, Space to play/pause.
Press / to search, d to download, w to toggle watched, q to quit.`,
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
