package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/vyto4ka/vpn/internal/panel/service"
	"github.com/vyto4ka/vpn/internal/panel/store"
	"github.com/vyto4ka/vpn/internal/panel/subscription"
	"github.com/vyto4ka/vpn/internal/xrayconf"
)

// DefaultPanelDataDir holds panel.db and the internal CA.
const DefaultPanelDataDir = "/var/lib/vpn"

var adminDataDir string

// openService opens the panel database for admin commands. It works while the panel runs:
// SQLite WAL allows concurrent access and the panel picks up changes from the outbox.
func openService(ctx context.Context) (*service.Service, func(), error) {
	if err := os.MkdirAll(adminDataDir, 0o700); err != nil {
		return nil, nil, err
	}
	st, err := store.Open(ctx, filepath.Join(adminDataDir, "panel.db"))
	if err != nil {
		return nil, nil, err
	}
	s := service.New(st)
	if err := s.EnsureDefaults(ctx); err != nil {
		st.Close()
		return nil, nil, err
	}
	return s, func() { st.Close() }, nil
}

// withService wraps a RunE that needs the service.
func withService(fn func(ctx context.Context, s *service.Service, cmd *cobra.Command, args []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		s, closeFn, err := openService(cmd.Context())
		if err != nil {
			return err
		}
		defer closeFn()
		return fn(cmd.Context(), s, cmd, args)
	}
}

func adminCmd() *cobra.Command {
	c := &cobra.Command{Use: "admin", Short: "Manage the panel from the command line (until the web UI exists)"}
	c.PersistentFlags().StringVar(&adminDataDir, "data-dir", DefaultPanelDataDir, "panel data directory")
	c.AddCommand(adminNodeCmd(), adminProfileCmd(), adminInboundCmd(), adminGroupCmd(), adminTemplateCmd(), adminUserCmd(), adminAuditCmd(), adminSettingCmd(), adminStatsCmd(), adminSetupCmd())
	return c
}

func table(w io.Writer, header string, rows [][]string) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, header)
	for _, r := range rows {
		fmt.Fprintln(tw, strings.Join(r, "\t"))
	}
	tw.Flush()
}

// parseSets turns ["K=V", ...] into a map; numeric values become ints.
func parseSets(sets []string) (map[string]any, error) {
	if len(sets) == 0 {
		return nil, nil
	}
	out := map[string]any{}
	for _, kv := range sets {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("--set expects NAME=VALUE, got %q", kv)
		}
		if n, err := strconv.Atoi(v); err == nil {
			out[k] = n
		} else if v == "" {
			out[k] = nil
		} else {
			out[k] = v
		}
	}
	return out, nil
}

func parseOverride(s string) (map[string]any, error) {
	if s == "" {
		return nil, nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, fmt.Errorf("--override must be a JSON object: %w", err)
	}
	return m, nil
}

func ts(p *int64) string {
	if p == nil {
		return "—"
	}
	return time.Unix(*p, 0).Format("2006-01-02 15:04")
}

// ---- nodes ----

func adminNodeCmd() *cobra.Command {
	c := &cobra.Command{Use: "node", Short: "Nodes"}

	var in service.NodeInput
	add := &cobra.Command{
		Use: "add", Short: "Add a node and print its install token",
		RunE: withService(func(ctx context.Context, s *service.Service, cmd *cobra.Command, _ []string) error {
			n, token, err := s.CreateNode(ctx, service.ActorCLI, in)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "node %s (id %d) created\n", n.Code, n.ID)
			return printJoinToken(ctx, cmd.OutOrStdout(), s, n, token)
		}),
	}
	add.Flags().StringVar(&in.Name, "name", "", "display name, e.g. Нидерланды")
	add.Flags().StringVar(&in.Country, "country", "", "ISO country code, e.g. NL")
	add.Flags().StringVar(&in.Domain, "domain", "", "node domain (Reality self-steal, A record to the node)")
	add.Flags().StringVar(&in.Code, "code", "", "node code (default: from country)")
	_ = add.MarkFlagRequired("name")

	list := &cobra.Command{
		Use: "list", Short: "List nodes with live numbers",
		RunE: withService(func(ctx context.Context, s *service.Service, cmd *cobra.Command, _ []string) error {
			nodes, err := s.NodeStatuses(ctx, nil)
			if err != nil {
				return err
			}
			var rows [][]string
			for _, ns := range nodes {
				n := ns.Node
				var state string
				switch {
				case !n.Enabled:
					state = "disabled"
				case !n.Local && n.CertSerial == "":
					state = "pending"
				case !ns.Connected:
					state = "offline"
				case n.AppliedHash != "" && n.AppliedHash == n.DesiredHash:
					state = "in sync"
				default:
					state = "syncing"
				}
				cpu, mem, online := "—", "—", "—"
				if m := ns.Metrics; m != nil {
					cpu = fmt.Sprintf("%.0f%%", m.CPU)
					if m.MemTotal > 0 {
						mem = fmt.Sprintf("%.0f%%", 100*float64(m.MemUsed)/float64(m.MemTotal))
					}
					online = strconv.FormatInt(m.Online, 10)
				}
				problems := n.LastError
				if len(n.Warnings) > 0 {
					problems = strings.TrimSpace(problems + " " + strings.Join(n.Warnings, "; "))
				}
				rows = append(rows, []string{n.Code, xrayconf.CountryFlag(n.Country) + " " + n.Name, n.Domain, state, ts(n.LastSeenAt),
					cpu, mem, online, gib(ns.TodayBytes), n.XrayVersion, problems})
			}
			table(cmd.OutOrStdout(), "CODE\tNAME\tDOMAIN\tSTATE\tLAST SEEN\tCPU\tRAM\tONLINE\tTODAY\tXRAY\tPROBLEMS", rows)
			return nil
		}),
	}

	token := &cobra.Command{
		Use: "token CODE", Short: "Revoke the node certificate and print a new install token", Args: cobra.ExactArgs(1),
		RunE: withService(func(ctx context.Context, s *service.Service, cmd *cobra.Command, args []string) error {
			n, err := s.NodeByCode(ctx, args[0])
			if err != nil {
				return err
			}
			t, err := s.ReissueInstallToken(ctx, service.ActorCLI, n.ID)
			if err != nil {
				return err
			}
			return printJoinToken(ctx, cmd.OutOrStdout(), s, n, t)
		}),
	}

	rm := &cobra.Command{
		Use: "rm CODE", Short: "Delete a node", Args: cobra.ExactArgs(1),
		RunE: withService(func(ctx context.Context, s *service.Service, _ *cobra.Command, args []string) error {
			n, err := s.NodeByCode(ctx, args[0])
			if err != nil {
				return err
			}
			return s.DeleteNode(ctx, service.ActorCLI, n.ID)
		}),
	}

	addrs := &cobra.Command{
		Use: "addr CODE [IP]", Short: "List node addresses, or add one by hand", Args: cobra.RangeArgs(1, 2),
		RunE: withService(func(ctx context.Context, s *service.Service, cmd *cobra.Command, args []string) error {
			n, err := s.NodeByCode(ctx, args[0])
			if err != nil {
				return err
			}
			if len(args) == 2 {
				if _, err := s.AddAddress(ctx, service.ActorCLI, n.ID, args[1], ""); err != nil {
					return err
				}
			}
			list, err := s.Addresses(ctx, n.ID)
			if err != nil {
				return err
			}
			var rows [][]string
			for _, a := range list {
				rows = append(rows, []string{strconv.FormatInt(a.ID, 10), a.IP, a.Interface, yes(a.OnInterface), yes(a.IsPrimary)})
			}
			table(cmd.OutOrStdout(), "ID\tIP\tIFACE\tON IFACE\tPRIMARY", rows)
			return nil
		}),
	}
	c.AddCommand(add, list, token, rm, addrs)
	return c
}

func yes(b bool) string {
	if b {
		return "yes"
	}
	return ""
}

// ---- profiles ----

func adminProfileCmd() *cobra.Command {
	c := &cobra.Command{Use: "profile", Short: "Profiles (shared inbound settings created from templates)"}

	templates := &cobra.Command{
		Use: "templates", Short: "List built-in templates and their variables",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ts, err := xrayconf.Templates()
			if err != nil {
				return err
			}
			for _, t := range ts {
				fmt.Fprintf(cmd.OutOrStdout(), "%s — %s\n  %s\n", t.ID, t.Title, t.Summary)
				for _, v := range t.Variables {
					fmt.Fprintf(cmd.OutOrStdout(), "    %-22s %-7s %-8s %s\n", v.Name, v.Scope, v.Source, v.Description)
				}
			}
			return nil
		},
	}

	var name, tpl, override string
	var sets []string
	var groups []string
	add := &cobra.Command{
		Use: "add", Short: "Create a profile from a template",
		RunE: withService(func(ctx context.Context, s *service.Service, cmd *cobra.Command, _ []string) error {
			vals, err := parseSets(sets)
			if err != nil {
				return err
			}
			over, err := parseOverride(override)
			if err != nil {
				return err
			}
			p, err := s.CreateProfile(ctx, service.ActorCLI, service.ProfileInput{Name: name, TemplateID: tpl, Values: vals, Override: over})
			if err != nil {
				return err
			}
			for _, gname := range groups {
				g, err := s.GroupByName(ctx, gname)
				if err != nil {
					return fmt.Errorf("group %q: %w", gname, err)
				}
				if err := s.GrantAccess(ctx, service.ActorCLI, g.ID, store.AccessProfile, p.ID); err != nil {
					return err
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "profile %q (id %d) created from %s\n", p.Name, p.ID, p.TemplateID)
			return nil
		}),
	}
	add.Flags().StringVar(&name, "name", "", "profile name")
	add.Flags().StringVar(&tpl, "template", "vless-reality-selfsteal", "template id (see `profile templates`)")
	add.Flags().StringArrayVar(&sets, "set", nil, "profile variable NAME=VALUE (repeatable)")
	add.Flags().StringVar(&override, "override", "", `JSON merge patch over the template inbound, e.g. '{"sniffing":{"enabled":false}}'`)
	add.Flags().StringArrayVar(&groups, "group", []string{"Основная"}, "grant these groups access to the whole profile")
	_ = add.MarkFlagRequired("name")

	var setVals []string
	var setOverride string
	set := &cobra.Command{
		Use: "set NAME", Short: "Change profile variables (applies to every node using it)", Args: cobra.ExactArgs(1),
		RunE: withService(func(ctx context.Context, s *service.Service, _ *cobra.Command, args []string) error {
			p, err := s.ProfileByName(ctx, args[0])
			if err != nil {
				return err
			}
			vals, err := parseSets(setVals)
			if err != nil {
				return err
			}
			over, err := parseOverride(setOverride)
			if err != nil {
				return err
			}
			_, err = s.UpdateProfile(ctx, service.ActorCLI, p.ID, service.ProfileInput{Values: vals, Override: over})
			return err
		}),
	}
	set.Flags().StringArrayVar(&setVals, "set", nil, "NAME=VALUE; empty value resets to default")
	set.Flags().StringVar(&setOverride, "override", "", "JSON merge patch replacing the profile override ('{}' clears it)")

	list := &cobra.Command{
		Use: "list", Short: "List profiles",
		RunE: withService(func(ctx context.Context, s *service.Service, cmd *cobra.Command, _ []string) error {
			ps, err := s.Profiles(ctx)
			if err != nil {
				return err
			}
			var rows [][]string
			for _, p := range ps {
				rows = append(rows, []string{strconv.FormatInt(p.ID, 10), p.Name, p.TemplateID, p.TagPattern, fmt.Sprint(p.Values)})
			}
			table(cmd.OutOrStdout(), "ID\tNAME\tTEMPLATE\tTAG\tVALUES", rows)
			return nil
		}),
	}
	c.AddCommand(templates, add, set, list)
	return c
}

// ---- node inbounds ----

func adminInboundCmd() *cobra.Command {
	c := &cobra.Command{Use: "inbound", Short: "Node inbounds (a profile on a node: own tag and keys)"}

	var node, profile, listen, egress, tag string
	var port int
	var sets []string
	attach := &cobra.Command{
		Use: "attach", Short: "Put a profile on a node",
		RunE: withService(func(ctx context.Context, s *service.Service, cmd *cobra.Command, _ []string) error {
			n, err := s.NodeByCode(ctx, node)
			if err != nil {
				return fmt.Errorf("node %s: %w", node, err)
			}
			p, err := s.ProfileByName(ctx, profile)
			if err != nil {
				return fmt.Errorf("profile %q: %w", profile, err)
			}
			vals, err := parseSets(sets)
			if err != nil {
				return err
			}
			in := service.AttachInput{NodeID: n.ID, ProfileID: p.ID, Values: vals, PortOverride: port, Tag: tag}
			if in.ListenAddressID, err = addressID(ctx, s, n.ID, listen); err != nil {
				return err
			}
			if in.EgressAddressID, err = addressID(ctx, s, n.ID, egress); err != nil {
				return err
			}
			ni, err := s.AttachProfile(ctx, service.ActorCLI, in)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "inbound %s created on %s\n", ni.Tag, n.Code)
			return nil
		}),
	}
	attach.Flags().StringVar(&node, "node", "", "node code")
	attach.Flags().StringVar(&profile, "profile", "", "profile name")
	attach.Flags().StringVar(&listen, "listen", "", "listen IP (must be a node address; default all interfaces)")
	attach.Flags().StringVar(&egress, "egress", "", "egress IP (default = listen IP)")
	attach.Flags().StringVar(&tag, "tag", "", "explicit tag (default from the profile pattern)")
	attach.Flags().IntVar(&port, "port", 0, "port override")
	attach.Flags().StringArrayVar(&sets, "set", nil, "node variable NAME=VALUE, e.g. CDN_DOMAIN=cdn.example.com")
	_ = attach.MarkFlagRequired("node")
	_ = attach.MarkFlagRequired("profile")

	list := &cobra.Command{
		Use: "list [NODE]", Short: "List node inbounds", Args: cobra.MaximumNArgs(1),
		RunE: withService(func(ctx context.Context, s *service.Service, cmd *cobra.Command, args []string) error {
			var nodeID int64
			if len(args) == 1 {
				n, err := s.NodeByCode(ctx, args[0])
				if err != nil {
					return err
				}
				nodeID = n.ID
			}
			list, err := s.NodeInbounds(ctx, nodeID)
			if err != nil {
				return err
			}
			var rows [][]string
			for _, ni := range list {
				r, err := s.RenderNodeInbound(ctx, ni.ID)
				listenAt, info := "", ""
				if err != nil {
					info = "ERROR: " + err.Error()
				} else {
					listenAt = fmt.Sprintf("%v:%v", r.Inbound["listen"], r.Inbound["port"])
					if pk, ok := r.Values["REALITY_PUBLIC_KEY"]; ok {
						info = fmt.Sprintf("pbk=%v sid=%v", pk, r.Values["REALITY_SHORT_ID"])
					}
				}
				rows = append(rows, []string{ni.Tag, listenAt, yes(ni.Enabled), info})
			}
			table(cmd.OutOrStdout(), "TAG\tLISTEN\tENABLED\tINFO", rows)
			return nil
		}),
	}

	show := &cobra.Command{
		Use: "show TAG", Short: "Print the rendered inbound JSON", Args: cobra.ExactArgs(1),
		RunE: withService(func(ctx context.Context, s *service.Service, cmd *cobra.Command, args []string) error {
			ni, err := s.NodeInboundByTag(ctx, args[0])
			if err != nil {
				return err
			}
			r, err := s.RenderNodeInbound(ctx, ni.ID)
			if err != nil {
				return err
			}
			b, _ := xrayconf.MarshalIndent(r.Inbound)
			fmt.Fprintln(cmd.OutOrStdout(), string(b))
			return nil
		}),
	}

	var setVals []string
	var regen bool
	set := &cobra.Command{
		Use: "set TAG", Short: "Change node variables or regenerate keys", Args: cobra.ExactArgs(1),
		RunE: withService(func(ctx context.Context, s *service.Service, _ *cobra.Command, args []string) error {
			ni, err := s.NodeInboundByTag(ctx, args[0])
			if err != nil {
				return err
			}
			vals, err := parseSets(setVals)
			if err != nil {
				return err
			}
			_, err = s.UpdateNodeInbound(ctx, service.ActorCLI, ni.ID, service.NodeInboundInput{Values: vals, RegenerateKeys: regen})
			return err
		}),
	}
	set.Flags().StringArrayVar(&setVals, "set", nil, "NAME=VALUE")
	set.Flags().BoolVar(&regen, "regenerate-keys", false, "new Reality keys and shortId")

	detach := &cobra.Command{
		Use: "detach TAG", Short: "Remove a node inbound", Args: cobra.ExactArgs(1),
		RunE: withService(func(ctx context.Context, s *service.Service, _ *cobra.Command, args []string) error {
			ni, err := s.NodeInboundByTag(ctx, args[0])
			if err != nil {
				return err
			}
			return s.DetachInbound(ctx, service.ActorCLI, ni.ID)
		}),
	}
	var hostSets []string
	host := &cobra.Command{
		Use: "host TAG", Short: "Show or override the connection point (remark, address, port, sni, fingerprint, hidden)", Args: cobra.ExactArgs(1),
		RunE: withService(func(ctx context.Context, s *service.Service, cmd *cobra.Command, args []string) error {
			ni, err := s.NodeInboundByTag(ctx, args[0])
			if err != nil {
				return err
			}
			if len(hostSets) > 0 {
				patch, err := parseSets(hostSets)
				if err != nil {
					return err
				}
				if v, ok := patch["hidden"]; ok {
					patch["hidden"] = v == "true" || v == 1
				}
				if err := s.SetHostOverride(ctx, service.ActorCLI, ni.ID, patch); err != nil {
					return err
				}
				if ni, err = s.NodeInboundByTag(ctx, args[0]); err != nil {
					return err
				}
			}
			h, err := s.HostFor(ctx, ni)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "remark   %s\naddress  %s:%d\nnetwork  %s/%s\nsni      %s\nfp       %s\nhidden   %v\noverride %v\n",
				h.Remark, h.Address, h.Port, h.Network, h.Security, h.SNI, h.Fingerprint, h.Hidden, ni.Host)
			return nil
		}),
	}
	host.Flags().StringArrayVar(&hostSets, "set", nil, "FIELD=VALUE; empty value returns the field to automatic")

	c.AddCommand(attach, list, show, set, detach, host)
	return c
}

func addressID(ctx context.Context, s *service.Service, nodeID int64, ip string) (*int64, error) {
	if ip == "" {
		return nil, nil
	}
	addrs, err := s.Addresses(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	for _, a := range addrs {
		if a.IP == ip {
			return &a.ID, nil
		}
	}
	a, err := s.AddAddress(ctx, service.ActorCLI, nodeID, ip, "")
	if err != nil {
		return nil, err
	}
	return &a.ID, nil
}

// ---- groups ----

func adminGroupCmd() *cobra.Command {
	c := &cobra.Command{Use: "group", Short: "Groups (internal squads)"}
	add := &cobra.Command{
		Use: "add NAME", Short: "Create a group", Args: cobra.ExactArgs(1),
		RunE: withService(func(ctx context.Context, s *service.Service, _ *cobra.Command, args []string) error {
			_, err := s.CreateGroup(ctx, service.ActorCLI, args[0], "")
			return err
		}),
	}
	grant := func(revoke bool) *cobra.Command {
		use, short := "grant", "Grant access: profile NAME | node CODE | inbound TAG"
		if revoke {
			use, short = "revoke", "Revoke access: profile NAME | node CODE | inbound TAG"
		}
		return &cobra.Command{
			Use: use + " GROUP KIND REF", Short: short, Args: cobra.ExactArgs(3),
			RunE: withService(func(ctx context.Context, s *service.Service, _ *cobra.Command, args []string) error {
				g, err := s.GroupByName(ctx, args[0])
				if err != nil {
					return fmt.Errorf("group %q: %w", args[0], err)
				}
				kind, ref, err := resolveRef(ctx, s, args[1], args[2])
				if err != nil {
					return err
				}
				if revoke {
					return s.RevokeAccess(ctx, service.ActorCLI, g.ID, kind, ref)
				}
				return s.GrantAccess(ctx, service.ActorCLI, g.ID, kind, ref)
			}),
		}
	}
	list := &cobra.Command{
		Use: "list", Short: "List groups and their access rules",
		RunE: withService(func(ctx context.Context, s *service.Service, cmd *cobra.Command, _ []string) error {
			gs, err := s.Groups(ctx)
			if err != nil {
				return err
			}
			var rows [][]string
			for _, g := range gs {
				rules, err := s.AccessRules(ctx, g.ID)
				if err != nil {
					return err
				}
				var rs []string
				for _, r := range rules {
					rs = append(rs, fmt.Sprintf("%s:%d", r.Kind, r.RefID))
				}
				rows = append(rows, []string{strconv.FormatInt(g.ID, 10), g.Name, strings.Join(rs, " ")})
			}
			table(cmd.OutOrStdout(), "ID\tNAME\tACCESS", rows)
			return nil
		}),
	}
	c.AddCommand(add, grant(false), grant(true), list)
	return c
}

func resolveRef(ctx context.Context, s *service.Service, kind, ref string) (string, int64, error) {
	switch kind {
	case "profile":
		p, err := s.ProfileByName(ctx, ref)
		if err != nil {
			return "", 0, fmt.Errorf("profile %q: %w", ref, err)
		}
		return store.AccessProfile, p.ID, nil
	case "node":
		n, err := s.NodeByCode(ctx, ref)
		if err != nil {
			return "", 0, fmt.Errorf("node %q: %w", ref, err)
		}
		return store.AccessNode, n.ID, nil
	case "inbound":
		ni, err := s.NodeInboundByTag(ctx, ref)
		if err != nil {
			return "", 0, fmt.Errorf("inbound %q: %w", ref, err)
		}
		return store.AccessNodeInbound, ni.ID, nil
	}
	return "", 0, fmt.Errorf("kind must be profile, node or inbound")
}

func groupIDs(ctx context.Context, s *service.Service, names []string) ([]int64, error) {
	var ids []int64
	for _, n := range names {
		g, err := s.GroupByName(ctx, n)
		if err != nil {
			return nil, fmt.Errorf("group %q: %w", n, err)
		}
		ids = append(ids, g.ID)
	}
	return ids, nil
}

// ---- user templates ----

func adminTemplateCmd() *cobra.Command {
	c := &cobra.Command{Use: "template", Short: "User templates (presets for new users)"}
	var in service.UserTemplateInput
	var groups []string
	var limitGB int64
	add := &cobra.Command{
		Use: "add NAME", Short: "Create a user template", Args: cobra.ExactArgs(1),
		RunE: withService(func(ctx context.Context, s *service.Service, _ *cobra.Command, args []string) error {
			in.Name = args[0]
			var err error
			if in.GroupIDs, err = groupIDs(ctx, s, groups); err != nil {
				return err
			}
			if limitGB > 0 {
				b := limitGB << 30
				in.TrafficLimitBytes = &b
			}
			_, err = s.CreateUserTemplate(ctx, service.ActorCLI, in)
			return err
		}),
	}
	add.Flags().IntVar(&in.ExpireMonths, "months", 0, "expires after N months")
	add.Flags().IntVar(&in.ExpireDays, "days", 0, "expires after N days")
	add.Flags().Int64Var(&limitGB, "limit-gb", 0, "traffic limit in GiB (0 = unlimited)")
	add.Flags().StringVar(&in.ResetStrategy, "reset", "month", "traffic reset: no|day|week|month")
	add.Flags().StringArrayVar(&groups, "group", nil, "group (repeatable)")
	add.Flags().BoolVar(&in.IsDefault, "default", false, "make it the default template")
	list := &cobra.Command{
		Use: "list", Short: "List user templates",
		RunE: withService(func(ctx context.Context, s *service.Service, cmd *cobra.Command, _ []string) error {
			ts, err := s.UserTemplates(ctx)
			if err != nil {
				return err
			}
			var rows [][]string
			for _, t := range ts {
				limit := "∞"
				if t.TrafficLimitBytes != nil {
					limit = fmt.Sprintf("%d GiB", *t.TrafficLimitBytes>>30)
				}
				rows = append(rows, []string{t.Name, yes(t.IsDefault), fmt.Sprintf("%dm %dd", t.ExpireMonths, t.ExpireDays), limit, t.ResetStrategy, fmt.Sprint(t.GroupIDs)})
			}
			table(cmd.OutOrStdout(), "NAME\tDEFAULT\tEXPIRES\tLIMIT\tRESET\tGROUPS", rows)
			return nil
		}),
	}
	c.AddCommand(add, list)
	return c
}

// ---- users ----

func adminUserCmd() *cobra.Command {
	c := &cobra.Command{Use: "user", Short: "Users"}

	var tplName string
	var groups []string
	add := &cobra.Command{
		Use: "add USERNAME", Short: "Create a user (everything else comes from the default template)", Args: cobra.ExactArgs(1),
		RunE: withService(func(ctx context.Context, s *service.Service, cmd *cobra.Command, args []string) error {
			in := service.CreateUserInput{Username: args[0]}
			if tplName != "" {
				t, err := s.UserTemplateByName(ctx, tplName)
				if err != nil {
					return fmt.Errorf("template %q: %w", tplName, err)
				}
				in.TemplateID = t.ID
			}
			if len(groups) > 0 {
				ids, err := groupIDs(ctx, s, groups)
				if err != nil {
					return err
				}
				in.GroupIDs = ids
			}
			u, err := s.CreateUser(ctx, service.ActorCLI, in)
			if err != nil {
				return err
			}
			printUser(cmd.OutOrStdout(), u)
			return nil
		}),
	}
	add.Flags().StringVar(&tplName, "template", "", "user template (default: the default one)")
	add.Flags().StringArrayVar(&groups, "group", nil, "groups instead of the template's")

	var status, search string
	list := &cobra.Command{
		Use: "list", Short: "List users",
		RunE: withService(func(ctx context.Context, s *service.Service, cmd *cobra.Command, _ []string) error {
			us, err := s.Users(ctx, store.UserFilter{Status: status, Search: search})
			if err != nil {
				return err
			}
			var rows [][]string
			for _, u := range us {
				rows = append(rows, []string{strconv.FormatInt(u.ID, 10), u.Username, u.Status, ts(u.ExpireAt), fmt.Sprintf("%.2f GiB", float64(u.TrafficUsedBytes)/(1<<30))})
			}
			table(cmd.OutOrStdout(), "ID\tUSERNAME\tSTATUS\tEXPIRES\tUSED", rows)
			return nil
		}),
	}
	list.Flags().StringVar(&status, "status", "", "active|disabled|limited|expired")
	list.Flags().StringVar(&search, "search", "", "substring of username or note")

	byName := func(use, short string, fn func(ctx context.Context, s *service.Service, u *store.User) (*store.User, error)) *cobra.Command {
		return &cobra.Command{
			Use: use + " USERNAME", Short: short, Args: cobra.ExactArgs(1),
			RunE: withService(func(ctx context.Context, s *service.Service, cmd *cobra.Command, args []string) error {
				u, err := s.UserByUsername(ctx, args[0])
				if err != nil {
					return fmt.Errorf("user %q: %w", args[0], err)
				}
				if u, err = fn(ctx, s, u); err != nil {
					return err
				}
				if u != nil {
					printUser(cmd.OutOrStdout(), u)
				}
				return nil
			}),
		}
	}

	var months, days int
	extend := byName("extend", "Extend from max(now, expiry)", func(ctx context.Context, s *service.Service, u *store.User) (*store.User, error) {
		return s.ExtendUser(ctx, service.ActorCLI, u.ID, months, days)
	})
	extend.Flags().IntVar(&months, "months", 0, "months")
	extend.Flags().IntVar(&days, "days", 0, "days")

	var setGroups []string
	groupsCmd := byName("groups", "Replace the user's groups", func(ctx context.Context, s *service.Service, u *store.User) (*store.User, error) {
		ids, err := groupIDs(ctx, s, setGroups)
		if err != nil {
			return nil, err
		}
		return s.SetUserGroups(ctx, service.ActorCLI, u.ID, ids)
	})
	groupsCmd.Flags().StringArrayVar(&setGroups, "group", nil, "group (repeatable)")

	var newToken, newUUID bool
	reissue := byName("reissue", "Issue a new subscription token and/or UUID", func(ctx context.Context, s *service.Service, u *store.User) (*store.User, error) {
		if !newToken && !newUUID {
			newToken = true
		}
		return s.ReissueUser(ctx, service.ActorCLI, u.ID, newToken, newUUID)
	})
	reissue.Flags().BoolVar(&newToken, "token", false, "new subscription token")
	reissue.Flags().BoolVar(&newUUID, "uuid", false, "new VLESS UUID")

	var linkAddr string
	links := &cobra.Command{
		Use: "links USERNAME", Short: "Print vless:// links to import into a client (preview until subscriptions)", Args: cobra.ExactArgs(1),
		RunE: withService(func(ctx context.Context, s *service.Service, cmd *cobra.Command, args []string) error {
			u, err := s.UserByUsername(ctx, args[0])
			if err != nil {
				return fmt.Errorf("user %q: %w", args[0], err)
			}
			if u.Status != store.StatusActive {
				fmt.Fprintf(cmd.OutOrStdout(), "warning: user is %s, nodes will reject it\n", u.Status)
			}
			hosts, err := s.UserHosts(ctx, u.ID)
			if err != nil {
				return err
			}
			if len(hosts) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no inbounds: check the user's groups and `vpn admin group list`")
			}
			for _, h := range hosts {
				if linkAddr != "" {
					h.Address = linkAddr
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s (%s):\n%s\n\n", h.Tag, h.Remark, subscription.Link(h, u.UUID))
			}
			return nil
		}),
	}

	links.Flags().StringVar(&linkAddr, "address", "", "server address to put in links instead of the node domain (e.g. its IP)")

	var rmDevice int64
	devices := &cobra.Command{
		Use: "devices USERNAME", Short: "List HWID devices, or free one with --rm ID", Args: cobra.ExactArgs(1),
		RunE: withService(func(ctx context.Context, s *service.Service, cmd *cobra.Command, args []string) error {
			u, err := s.UserByUsername(ctx, args[0])
			if err != nil {
				return fmt.Errorf("user %q: %w", args[0], err)
			}
			if rmDevice != 0 {
				if err := s.DeleteDevice(ctx, service.ActorCLI, u.ID, rmDevice); err != nil {
					return err
				}
			}
			ds, err := s.Devices(ctx, u.ID)
			if err != nil {
				return err
			}
			var rows [][]string
			for _, d := range ds {
				last := d.LastSeen
				rows = append(rows, []string{strconv.FormatInt(d.ID, 10), d.Model, d.Platform + " " + d.OSVersion, ts(&last), d.LastIP, d.UserAgent})
			}
			table(cmd.OutOrStdout(), "ID\tMODEL\tOS\tLAST SEEN\tIP\tAPP", rows)
			return nil
		}),
	}
	devices.Flags().Int64Var(&rmDevice, "rm", 0, "device id to remove")

	c.AddCommand(add, list, extend, groupsCmd, reissue, links, devices,
		&cobra.Command{
			Use: "show USERNAME", Short: "Show a user with traffic per node and the subscription URL", Args: cobra.ExactArgs(1),
			RunE: withService(func(ctx context.Context, s *service.Service, cmd *cobra.Command, args []string) error {
				u, err := s.UserByUsername(ctx, args[0])
				if err != nil {
					return fmt.Errorf("user %q: %w", args[0], err)
				}
				w := cmd.OutOrStdout()
				printUser(w, u)
				online := "never"
				if u.OnlineAt != nil {
					online = ts(u.OnlineAt)
				}
				fmt.Fprintf(w, "online    %s\n", online)
				if url, err := s.SubscriptionURL(ctx, u); err == nil {
					fmt.Fprintf(w, "sub url   %s\n", url)
				} else {
					fmt.Fprintf(w, "sub url   — (%v)\n", err)
				}
				byNode, err := s.UserTrafficByNode(ctx, u.ID, 30)
				if err != nil {
					return err
				}
				if len(byNode) > 0 {
					fmt.Fprintln(w, "\ntraffic per node, 30 days:")
					var rows [][]string
					for _, t := range byNode {
						rows = append(rows, []string{t.Code, gib(t.Bytes)})
					}
					table(w, "NODE\tTRAFFIC", rows)
				}
				return nil
			}),
		},
		byName("disable", "Disable a user", func(ctx context.Context, s *service.Service, u *store.User) (*store.User, error) {
			return s.SetUserEnabled(ctx, service.ActorCLI, u.ID, false)
		}),
		byName("enable", "Enable a user", func(ctx context.Context, s *service.Service, u *store.User) (*store.User, error) {
			return s.SetUserEnabled(ctx, service.ActorCLI, u.ID, true)
		}),
		byName("reset", "Reset the traffic counter", func(ctx context.Context, s *service.Service, u *store.User) (*store.User, error) {
			return s.ResetUserTraffic(ctx, service.ActorCLI, u.ID)
		}),
		byName("rm", "Delete a user", func(ctx context.Context, s *service.Service, u *store.User) (*store.User, error) {
			return nil, s.DeleteUser(ctx, service.ActorCLI, u.ID)
		}),
	)
	return c
}

func gib(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.2f GiB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(b)/(1<<20))
	}
	return fmt.Sprintf("%d KiB", b>>10)
}

func adminStatsCmd() *cobra.Command {
	return &cobra.Command{
		Use: "stats", Short: "Dashboard: users, online, traffic, top users",
		RunE: withService(func(ctx context.Context, s *service.Service, cmd *cobra.Command, _ []string) error {
			o, err := s.Overview(ctx)
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "users: %d active, %d limited, %d expired, %d disabled\nonline now: %d\ntraffic: %s today, %s last 30 days\n\ntop users (30 days):\n",
				o.UsersByStatus[store.StatusActive], o.UsersByStatus[store.StatusLimited], o.UsersByStatus[store.StatusExpired], o.UsersByStatus[store.StatusDisabled],
				o.OnlineNow, gib(o.TodayBytes), gib(o.MonthBytes))
			var rows [][]string
			for _, u := range o.TopUsers {
				rows = append(rows, []string{u.Username, gib(u.Bytes)})
			}
			table(w, "USER\tTRAFFIC", rows)
			return nil
		}),
	}
}

func adminSetupCmd() *cobra.Command {
	var in service.SetupInput
	c := &cobra.Command{
		Use:   "setup",
		Short: "Configure an all-in-one server: local node, Reality self-steal, subscriptions (idempotent)",
		RunE: withService(func(ctx context.Context, s *service.Service, cmd *cobra.Command, _ []string) error {
			res, err := s.SetupAllInOne(ctx, service.ActorCLI, in)
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			for _, c := range res.Created {
				fmt.Fprintln(w, "created", c)
			}
			fmt.Fprintf(w, "node %s (%s), inbound %s, subscriptions on https://%s\n", res.Node.Code, res.Node.Domain, res.Inbound.Tag, firstNonEmptyStr(in.SubDomain, in.Domain))
			return nil
		}),
	}
	c.Flags().StringVar(&in.Domain, "domain", "", "node domain (A record to this server)")
	c.Flags().StringVar(&in.SubDomain, "sub-domain", "", "subscription domain (default: the node domain)")
	c.Flags().StringVar(&in.Email, "email", "", "email for Let's Encrypt (optional)")
	c.Flags().StringVar(&in.NodeName, "name", "", "node name shown in clients")
	c.Flags().StringVar(&in.Country, "country", "", "2-letter country code (flag in clients)")
	_ = c.MarkFlagRequired("domain")
	return c
}

func firstNonEmptyStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func printUser(w io.Writer, u *store.User) {
	limit := "∞"
	if u.TrafficLimitBytes != nil {
		limit = fmt.Sprintf("%.2f GiB", float64(*u.TrafficLimitBytes)/(1<<30))
	}
	fmt.Fprintf(w, "user      %s (id %d)\nstatus    %s\nexpires   %s\ntraffic   %.2f GiB / %s (reset: %s)\nuuid      %s\nsub token %s\n",
		u.Username, u.ID, u.Status, ts(u.ExpireAt), float64(u.TrafficUsedBytes)/(1<<30), limit, u.ResetStrategy, u.UUID, u.SubToken)
}

// ---- audit ----

func adminAuditCmd() *cobra.Command {
	var n int
	c := &cobra.Command{
		Use: "audit", Short: "Show recent changes",
		RunE: withService(func(ctx context.Context, s *service.Service, cmd *cobra.Command, _ []string) error {
			es, err := store.ListAudit(ctx, s.Store().DB, n)
			if err != nil {
				return err
			}
			sort.Slice(es, func(i, j int) bool { return es[i].ID < es[j].ID })
			var rows [][]string
			for _, e := range es {
				ts := e.TS
				rows = append(rows, []string{time.Unix(ts, 0).Format("01-02 15:04:05"), e.Actor + ":" + e.ActorID, e.Action, fmt.Sprintf("%s#%d", e.Entity, e.EntityID), e.Diff})
			}
			table(cmd.OutOrStdout(), "TIME\tACTOR\tACTION\tENTITY\tDIFF", rows)
			return nil
		}),
	}
	c.Flags().IntVarP(&n, "limit", "n", 30, "entries")
	return c
}
