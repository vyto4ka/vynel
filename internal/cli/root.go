// Package cli wires the vpn subcommands.
package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/vyto4ka/vpn/internal/buildinfo"
)

var logLevel string

// Execute runs the CLI and returns the process exit code.
func Execute() int {
	root := &cobra.Command{
		Use:           "vpn",
		Short:         "VPN panel and node (Xray + Caddy)",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(*cobra.Command, []string) error {
			var lvl slog.Level
			if err := lvl.UnmarshalText([]byte(logLevel)); err != nil {
				return fmt.Errorf("bad --log-level: %w", err)
			}
			slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl})))
			return nil
		},
	}
	root.PersistentFlags().StringVar(&logLevel, "log-level", "info", "debug|info|warn|error")
	root.AddCommand(versionCmd(), panelCmd(), nodeCmd(), adminCmd(), installCmd())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := root.ExecuteContext(ctx); err != nil {
		if errors.Is(err, context.Canceled) {
			return 0
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintln(cmd.OutOrStdout(), "vpn", buildinfo.String())
		},
	}
}

func installCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Interactive installer (stage 9, docs/INSTALL.md)",
		RunE: func(*cobra.Command, []string) error {
			return errors.New("the installer is not implemented yet (roadmap stage 9)")
		},
	}
}
