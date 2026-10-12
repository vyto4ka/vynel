package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/vyto4ka/vynel/internal/buildinfo"
	"github.com/vyto4ka/vynel/internal/update"
)

func updateCmd() *cobra.Command {
	var foreground, check bool
	c := &cobra.Command{
		Use:   "update",
		Short: "Update vynel, Xray and Caddy on this server (data is kept)",
		Long: "Runs the installer in update mode from the branch this server was installed from\n" +
			"(/etc/vynel/install.env). By default it starts in the background as the systemd unit\n" +
			"vynel-update and returns: follow it with `journalctl -u vynel-update -f`.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			w := cmd.OutOrStdout()
			if check {
				s := update.LoadSource()
				latest, err := update.Latest(cmd.Context(), s)
				if err != nil {
					return err
				}
				fmt.Fprintf(w, "running %s, latest in %s: %s\n", buildinfo.Version, s.Release, latest)
				return nil
			}
			if foreground {
				return update.Run(cmd.Context(), w)
			}
			if err := update.Start(cmd.Context()); err != nil {
				return err
			}
			fmt.Fprintln(w, "the update is running in the background: journalctl -u vynel-update -f")
			return nil
		},
	}
	c.Flags().BoolVar(&foreground, "foreground", false, "run here and wait (the services restart; over SSH that is fine)")
	c.Flags().BoolVar(&check, "check", false, "only show the running and the latest version")
	return c
}
