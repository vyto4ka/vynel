package cli

import "github.com/spf13/cobra"

func panelCmd() *cobra.Command {
	return &cobra.Command{Use: "panel", Short: "Run the panel"}
}
