package cli

import "github.com/spf13/cobra"

func nodeCmd() *cobra.Command {
	return &cobra.Command{Use: "node", Short: "Run the node agent"}
}
