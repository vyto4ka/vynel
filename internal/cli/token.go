package cli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/vyto4ka/vynnel/internal/jointoken"
	"github.com/vyto4ka/vynnel/internal/panel/ca"
	"github.com/vyto4ka/vynnel/internal/panel/service"
	"github.com/vyto4ka/vynnel/internal/panel/store"
)

// printJoinToken prints the one-line join token and the command to run on the node.
func printJoinToken(ctx context.Context, w io.Writer, s *service.Service, n *store.Node, secret string) error {
	addr, err := s.Setting(ctx, service.SettingGatewayAddr, "")
	if err != nil {
		return err
	}
	sni, err := s.Setting(ctx, service.SettingGatewaySNI, "")
	if err != nil {
		return err
	}
	if addr == "" || sni == "" {
		return fmt.Errorf("the gateway address is unknown: start the panel with --public-addr HOST:PORT "+
			"(or `vynnel admin setting gateway.addr HOST:PORT`), then run `vynnel admin node token %s`", n.Code)
	}
	authority, err := ca.LoadOrCreate(filepath.Join(adminDataDir, "ca"))
	if err != nil {
		return err
	}
	tok := jointoken.Token{Addr: addr, SNI: sni, CAFingerprint: authority.Fingerprint(), Secret: secret}.Encode()
	fmt.Fprintf(w, "join token for %s (valid %s, single use):\n\n  %s\n\non the node run:\n\n  vynnel node run --token %s\n",
		n.Code, service.InstallTokenTTL, tok, tok)
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
