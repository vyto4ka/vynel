package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/vyto4ka/vynel/internal/buildinfo"
	"github.com/vyto4ka/vynel/internal/update"
)

func updateCmd() *cobra.Command {
	var foreground, check bool
	var channel, ref string
	c := &cobra.Command{
		Use:   "update",
		Short: "Update vynel, Xray and nginx on this server (data is kept)",
		Long: "Runs the installer of the release this server follows (/etc/vynel/install.env) in update\n" +
			"mode. By default it starts in the background as the systemd unit vynel-update and\n" +
			"returns: follow it with `journalctl -u vynel-update -f`.\n\n" +
			"Channels: stable (the newest published release, the default for new installs) and dev\n" +
			"(the build CI makes from every push to a branch, to try a commit on this server before\n" +
			"it is released). `--channel dev --ref BRANCH` switches this server to a branch's builds,\n" +
			"`--channel stable` back to the releases.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			w := cmd.OutOrStdout()
			s := update.LoadSource()
			if channel != "" || ref != "" {
				if channel == "" {
					channel = s.Channel() // --ref alone: another branch of the dev channel
					if channel != update.ChannelDev {
						return errors.New("--ref is for --channel dev")
					}
				}
				next, err := s.WithChannel(channel, ref)
				if err != nil {
					return err
				}
				s = next
				if check {
					fmt.Fprintf(w, "would switch to %s (%s)\n", s.Channel(), s.Release)
				} else if err := update.SaveSource(s); err != nil {
					return fmt.Errorf("cannot save the channel (run as root): %w", err)
				} else {
					fmt.Fprintf(w, "this server now follows the %s channel (%s)\n", s.Channel(), s.Release)
				}
			}
			if check {
				latest, err := update.Latest(cmd.Context(), s)
				if err != nil {
					return err
				}
				fmt.Fprintf(w, "running %s, latest in %s channel (%s): %s\n", buildinfo.Version, s.Channel(), s.Release, latest)
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
	c.Flags().StringVar(&channel, "channel", "", "switch to a channel first: stable (releases) or dev (builds of a branch)")
	c.Flags().StringVar(&ref, "ref", "", "branch of the dev channel (with --channel dev; alone: another branch)")
	return c
}
