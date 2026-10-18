package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/vyto4ka/vynel/internal/node/agent"
	"github.com/vyto4ka/vynel/internal/setup"
)

// Extra addresses live in one file and are put on the interfaces by a boot-time unit, the same
// on netplan, ifupdown and NetworkManager servers (docs/INBOUNDS.md §2.6).
const (
	ipsConf = "/etc/vynel/ips.conf"
	ipsUnit = "/etc/systemd/system/vynel-ips.service"
)

type extraIP struct{ CIDR, Dev string }

func readIPs() ([]extraIP, error) {
	b, err := os.ReadFile(ipsConf)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []extraIP
	for _, l := range strings.Split(string(b), "\n") {
		if f := strings.Fields(l); len(f) == 2 && !strings.HasPrefix(f[0], "#") {
			out = append(out, extraIP{f[0], f[1]})
		}
	}
	return out, nil
}

func writeIPs(ips []extraIP) error {
	var b strings.Builder
	b.WriteString("# extra addresses put on the interfaces at boot by vynel-ips.service (vynel net add-ip / rm-ip)\n")
	for _, x := range ips {
		fmt.Fprintf(&b, "%s %s\n", x.CIDR, x.Dev)
	}
	if err := os.MkdirAll("/etc/vynel", 0o755); err != nil {
		return err
	}
	return os.WriteFile(ipsConf, []byte(b.String()), 0o644)
}

func ensureIPsUnit() error {
	unit := `[Unit]
Description=vynel: extra IP addresses (vynel net add-ip)
After=network-online.target
Wants=network-online.target
Before=vynel.service vynel-node.service

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/local/bin/vynel net apply

[Install]
WantedBy=multi-user.target
`
	if b, err := os.ReadFile(ipsUnit); err == nil && string(b) == unit {
		return nil
	}
	if err := os.WriteFile(ipsUnit, []byte(unit), 0o644); err != nil {
		return err
	}
	_ = exec.Command("systemctl", "daemon-reload").Run()
	return exec.Command("systemctl", "enable", "vynel-ips.service").Run()
}

func ipCmd(ctx context.Context, args ...string) error {
	out, err := exec.CommandContext(ctx, "ip", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("ip %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return nil
}

// normCIDR turns "203.0.113.12" into "203.0.113.12/32" (/128 for IPv6).
func normCIDR(s string) (string, net.IP, error) {
	if !strings.Contains(s, "/") {
		ip := net.ParseIP(s)
		if ip == nil {
			return "", nil, fmt.Errorf("%q is not an IP address", s)
		}
		if ip.To4() != nil {
			return ip.String() + "/32", ip, nil
		}
		return ip.String() + "/128", ip, nil
	}
	ip, n, err := net.ParseCIDR(s)
	if err != nil {
		return "", nil, err
	}
	ones, _ := n.Mask.Size()
	return fmt.Sprintf("%s/%d", ip, ones), ip, nil
}

func primaryDev() string {
	for _, a := range agent.LocalAddresses() {
		if a.Primary {
			return a.Interface
		}
	}
	return ""
}

// outbound checks that traffic from ip reaches the internet (the hoster routes it here).
func outbound(ip net.IP) error {
	d := net.Dialer{Timeout: 5 * time.Second, LocalAddr: &net.TCPAddr{IP: ip}}
	target := "1.1.1.1:443"
	if ip.To4() == nil {
		target = "[2606:4700:4700::1111]:443"
	}
	c, err := d.Dial("tcp", target)
	if err != nil {
		return err
	}
	return c.Close()
}

func netCmd() *cobra.Command {
	c := &cobra.Command{Use: "net", Short: "Server addresses: list, add an extra IP with automatic rollback"}

	list := &cobra.Command{
		Use: "list", Short: "Addresses on the interfaces",
		RunE: func(cmd *cobra.Command, _ []string) error {
			extra, _ := readIPs()
			kept := map[string]bool{}
			for _, x := range extra {
				ip, _, _ := strings.Cut(x.CIDR, "/")
				kept[ip] = true
			}
			w := cmd.OutOrStdout()
			for _, a := range agent.LocalAddresses() {
				note := ""
				if a.Primary {
					note += "  primary (default route)"
				}
				if kept[a.Ip] {
					note += "  added by vynel net add-ip"
				}
				fmt.Fprintf(w, "%-40s %-10s%s\n", a.Ip, a.Interface, note)
			}
			return nil
		},
	}

	var dev string
	var wait time.Duration
	var yes bool
	add := &cobra.Command{
		Use:   "add-ip IP[/PREFIX]",
		Short: "Put an extra IP on the interface; rolled back unless you confirm that the server is still reachable",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			w := cmd.OutOrStdout()
			cidr, ip, err := normCIDR(args[0])
			if err != nil {
				return err
			}
			if dev == "" {
				if dev = primaryDev(); dev == "" {
					return errors.New("cannot find the main interface: pass --dev")
				}
			}
			for _, a := range agent.LocalAddresses() {
				if a.Ip == ip.String() {
					return fmt.Errorf("%s is already on %s", ip, a.Interface)
				}
			}
			if err := ipCmd(ctx, "addr", "add", cidr, "dev", dev); err != nil {
				return err
			}
			fmt.Fprintf(w, "added %s on %s\n", cidr, dev)
			rollback := func(why string) error {
				_ = ipCmd(context.Background(), "addr", "del", cidr, "dev", dev)
				return fmt.Errorf("rolled back: %s", why)
			}
			if err := outbound(ip); err != nil {
				fmt.Fprintf(w, "warning: traffic from %s does not reach the internet yet (%v).\n  The hoster may not route it to this server yet, or it needs a gateway of its own.\n", ip, err)
			} else {
				fmt.Fprintf(w, "traffic from %s reaches the internet\n", ip)
			}
			if !yes {
				// Like `netplan try`: a lost SSH session never answers, and the address goes away.
				signal.Ignore(syscall.SIGHUP)
				fmt.Fprintf(w, "\nIs the server still reachable (this SSH session works)? Type yes within %s, otherwise it is rolled back: ", wait)
				answer := make(chan string, 1)
				go func() {
					line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
					answer <- strings.TrimSpace(strings.ToLower(line))
				}()
				select {
				case a := <-answer:
					if a != "yes" && a != "y" && a != "да" {
						return rollback("not confirmed")
					}
				case <-time.After(wait):
					fmt.Fprintln(w)
					return rollback("no answer in " + wait.String())
				}
			}
			ips, err := readIPs()
			if err != nil {
				return err
			}
			ips = append(ips, extraIP{cidr, dev})
			if err := writeIPs(ips); err != nil {
				return err
			}
			if err := ensureIPsUnit(); err != nil {
				fmt.Fprintf(w, "warning: the address will not come back after a reboot: %v\n", err)
			}
			fmt.Fprintf(w, "kept %s: it survives reboots (vynel-ips.service). The panel sees it within a minute: Ноды → адреса.\n", cidr)
			return nil
		},
	}
	add.Flags().StringVar(&dev, "dev", "", "interface (default: the one with the default route)")
	add.Flags().DurationVar(&wait, "timeout", 60*time.Second, "roll back unless confirmed within this time")
	add.Flags().BoolVar(&yes, "yes", false, "keep it without asking")

	rm := &cobra.Command{
		Use: "rm-ip IP", Short: "Remove an address added with add-ip", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, ip, err := normCIDR(args[0])
			if err != nil {
				return err
			}
			ips, err := readIPs()
			if err != nil {
				return err
			}
			var keep []extraIP
			found := false
			for _, x := range ips {
				if strings.HasPrefix(x.CIDR, ip.String()+"/") {
					found = true
					_ = ipCmd(cmd.Context(), "addr", "del", x.CIDR, "dev", x.Dev)
					continue
				}
				keep = append(keep, x)
			}
			if !found {
				return fmt.Errorf("%s was not added with vynel net add-ip", ip)
			}
			if err := writeIPs(keep); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed %s\n", ip)
			return nil
		},
	}

	apply := &cobra.Command{
		Use: "apply", Short: "Put the added addresses on the interfaces (run at boot by vynel-ips.service)", Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ips, err := readIPs()
			if err != nil {
				return err
			}
			have := map[string]bool{}
			for _, a := range agent.LocalAddresses() {
				have[a.Ip] = true
			}
			var errs []error
			for _, x := range ips {
				ip, _, _ := strings.Cut(x.CIDR, "/")
				if have[ip] {
					continue
				}
				if err := ipCmd(cmd.Context(), "addr", "add", x.CIDR, "dev", x.Dev); err != nil {
					errs = append(errs, err)
				}
			}
			return errors.Join(errs...)
		},
	}
	resolve := &cobra.Command{
		Use: "resolve DOMAIN", Short: "A records as public DNS sees them (not /etc/hosts)", Args: cobra.ExactArgs(1), Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ips, err := setup.LookupA(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), strings.Join(ips, "\n"))
			return nil
		},
	}
	c.AddCommand(list, add, rm, apply, resolve)
	return c
}
