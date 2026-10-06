package cli

import "github.com/spf13/cobra"

func adminCmd() *cobra.Command {
	return &cobra.Command{Use: "admin", Short: "Administrative commands"}
}
