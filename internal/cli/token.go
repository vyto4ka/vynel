package cli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/vyto4ka/vynel/internal/panel/ca"
	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
)

// printJoinToken prints the one-line join token and the command to run on the node.
func printJoinToken(ctx context.Context, w io.Writer, s *service.Service, n *store.Node, secret string) error {
	authority, err := ca.LoadOrCreate(filepath.Join(adminDataDir, "ca"))
	if err != nil {
		return err
	}
	tok, err := s.JoinToken(ctx, authority.Fingerprint(), secret)
	if err != nil {
		return fmt.Errorf("%w; then run `vynel admin node token %s`", err, n.Code)
	}
	fmt.Fprintf(w, "join token for %s (valid %s, single use):\n\n  %s\n\non the new server (root, Ubuntu/Debian, ports 80 and 443 free) run:\n\n  %s\n",
		n.Code, service.InstallTokenTTL, tok, s.NodeInstallCommand(ctx, tok))
	return nil
}

func adminSettingCmd() *cobra.Command {
	return &cobra.Command{
		Use: "setting KEY [VALUE]", Short: "Read or change a setting", Args: cobra.RangeArgs(1, 2),
		RunE: withService(func(ctx context.Context, s *service.Service, cmd *cobra.Command, args []string) error {
			if len(args) == 2 {
				return s.SetSetting(ctx, service.ActorCLI, args[0], args[1])
			}
			v, err := s.Setting(ctx, args[0], "")
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), v)
			return nil
		}),
	}
}
