package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/vyto4ka/vynel/internal/node/firewall"
)

// firewallCmd switches the managed nftables firewall (docs/INSTALL_GUIDE.md §7). The running
// panel or node picks the change up within 15 seconds.
func firewallCmd() *cobra.Command {
	c := &cobra.Command{Use: "firewall", Short: "Firewall: only SSH and what vynel serves is open, port scanners are banned"}

	var noTrap, noPing bool
	var allow []string
	on := &cobra.Command{
		Use: "on", Short: "Turn the firewall on",
		RunE: func(cmd *cobra.Command, _ []string) error {
			conf, err := firewall.LoadConf()
			if err != nil {
				return err
			}
			for _, a := range allow {
				if _, err := firewall.ParsePort(a); err != nil {
					return err
				}
				if !slices.Contains(conf.Allow, a) {
					conf.Allow = append(conf.Allow, a)
				}
			}
			conf.Enabled, conf.NoTrap, conf.NoPing = true, noTrap, noPing
			if err := firewall.SaveConf(conf); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "firewall on: rules load within 15 s (vynel or vynel-node must be running); check: vynel firewall status")
			return nil
		},
	}
	on.Flags().BoolVar(&noTrap, "no-trap", false, "do not ban addresses that scan closed ports")
	on.Flags().BoolVar(&noPing, "no-ping", false, "do not answer ping")
	on.Flags().StringSliceVar(&allow, "allow", nil, "extra ports to open, e.g. 8080,5000/udp")

	off := &cobra.Command{
		Use: "off", Short: "Turn the firewall off and remove its rules now",
		RunE: func(cmd *cobra.Command, _ []string) error {
			conf, _ := firewall.LoadConf()
			conf.Enabled = false
			if err := firewall.SaveConf(conf); err != nil {
				return err
			}
			if err := firewall.Remove(cmd.Context()); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "firewall off: rules removed")
			return nil
		},
	}

	editAllow := func(add bool) func(cmd *cobra.Command, args []string) error {
		return func(cmd *cobra.Command, args []string) error {
			conf, err := firewall.LoadConf()
			if err != nil {
				return err
			}
			for _, a := range args {
				if _, err := firewall.ParsePort(a); err != nil {
					return err
				}
				conf.Allow = slices.DeleteFunc(conf.Allow, func(x string) bool { return x == a })
				if add {
					conf.Allow = append(conf.Allow, a)
				}
			}
			if err := firewall.SaveConf(conf); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "extra ports: %s\n", strings.Join(conf.Allow, " "))
			return nil
		}
	}
	allowCmd := &cobra.Command{Use: "allow PORT[/udp]...", Short: "Open extra ports (for your own services)", Args: cobra.MinimumNArgs(1), RunE: editAllow(true)}
	deny := &cobra.Command{Use: "deny PORT[/udp]...", Short: "Close ports opened with allow", Args: cobra.MinimumNArgs(1), RunE: editAllow(false)}

	unblock := &cobra.Command{
		Use: "unblock IP|all", Short: "Lift the scanner ban from an address (or everyone)", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := firewall.Unblock(cmd.Context(), args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s unblocked\n", args[0])
			return nil
		},
	}

	status := &cobra.Command{
		Use: "status", Short: "Show what is open and who is banned",
		RunE: func(cmd *cobra.Command, _ []string) error { return firewallStatus(cmd.Context(), cmd) },
	}
	c.AddCommand(status, on, off, allowCmd, deny, unblock)
	return c
}

func firewallStatus(ctx context.Context, cmd *cobra.Command) error {
	w := cmd.OutOrStdout()
	conf, err := firewall.LoadConf()
	if err != nil {
		return err
	}
	loaded := firewall.Present(ctx)
	state := "off"
	switch {
	case conf.Enabled && loaded:
		state = "on"
	case conf.Enabled:
		state = "on, but the rules are not loaded yet (is vynel / vynel-node running? journalctl -u vynel -u vynel-node | grep firewall)"
	case loaded:
		state = "off, the rules are being removed"
	}
	fmt.Fprintf(w, "firewall   %s\n", state)
	if !loaded {
		if _, err := exec.LookPath("nft"); err != nil {
			fmt.Fprintln(w, "nft        not installed (apt install nftables)")
		}
		return nil
	}
	script, _ := os.ReadFile(firewall.AppliedPath())
	ssh := firewall.SSHPortsString(string(script))
	fmt.Fprintf(w, "ssh        %s (new connections limited per address)\n", ssh)
	for _, l := range strings.Split(string(script), "\n") {
		l = strings.TrimSpace(l)
		if strings.HasSuffix(l, " accept") && !strings.Contains(l, "meter") && (strings.HasPrefix(l, "tcp dport") || strings.HasPrefix(l, "udp dport")) {
			proto, rest, _ := strings.Cut(l, " dport ")
			if rest = strings.TrimSuffix(rest, " accept"); proto == "tcp" && rest == ssh {
				continue
			}
			fmt.Fprintf(w, "open %s   %s\n", proto, rest)
		}
	}
	if len(conf.Allow) > 0 {
		fmt.Fprintf(w, "by hand    %s\n", strings.Join(conf.Allow, " "))
	}
	fmt.Fprintf(w, "ping       %s\n", map[bool]string{true: "not answered", false: "answered (rate-limited)"}[conf.NoPing])
	if conf.NoTrap {
		fmt.Fprintln(w, "scanners   not banned (--no-trap)")
		return nil
	}
	banned := firewall.Banned(ctx)
	for i, e := range banned {
		if i == 20 {
			break
		}
		fmt.Fprintf(w, "banned     %s\n", e)
	}
	n := len(banned)
	fmt.Fprintf(w, "scanners   %d banned for a day; lift: vynel firewall unblock IP|all\n", n)
	return nil
}
