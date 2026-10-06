package cli

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/vyto4ka/vynel/internal/buildinfo"
	"github.com/vyto4ka/vynel/internal/node/agent"
	"github.com/vyto4ka/vynel/internal/xray"
)

// DefaultNodeDataDir holds the node credentials and state.
const DefaultNodeDataDir = "/var/lib/vynel-node"

func nodeCmd() *cobra.Command {
	var dataDir string
	c := &cobra.Command{Use: "node", Short: "Node agent"}
	c.PersistentFlags().StringVar(&dataDir, "data-dir", DefaultNodeDataDir, "node data directory")

	join := &cobra.Command{
		Use: "join TOKEN", Short: "Register this node with the panel using a join token", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			code, err := agent.Join(cmd.Context(), dataDir, args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "joined as node %s; now run `vynel node run`\n", code)
			return nil
		},
	}

	var token, caddyBin string
	var tune bool
	var bin xray.Binary
	run := &cobra.Command{
		Use: "run", Short: "Run the node agent",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !agent.Joined(dataDir) {
				if token == "" {
					return errors.New("node is not joined: pass --token or run `vynel node join TOKEN`")
				}
				code, err := agent.Join(cmd.Context(), dataDir, token)
				if err != nil {
					return err
				}
				slog.Info("joined the panel", "node", code)
			}
			dial, closeFn, err := agent.GRPCDialer(dataDir)
			if err != nil {
				return err
			}
			defer closeFn()
			a, err := agent.New(agent.Config{DataDir: dataDir, Xray: bin, Version: buildinfo.Version, Dial: dial, Log: slog.Default(),
				CaddyBin: caddyBin, TuneSysctl: tune})
			if err != nil {
				return err
			}
			defer a.Close()
			return a.Run(cmd.Context())
		},
	}
	run.Flags().StringVar(&token, "token", "", "join token, used only if the node is not joined yet")
	addXrayFlags(run, &bin)
	run.Flags().StringVar(&caddyBin, "caddy-bin", "/usr/local/bin/caddy", "path to the caddy binary")
	run.Flags().BoolVar(&tune, "tune-sysctl", true, "apply BBR/fq/TCP Fast Open (needs root)")

	setPanel := &cobra.Command{
		Use: "set-panel HOST:PORT", Short: "Point the node to a moved panel (same CA)", Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error { return agent.SetPanelAddr(dataDir, args[0]) },
	}
	c.AddCommand(join, run, setPanel)
	return c
}
