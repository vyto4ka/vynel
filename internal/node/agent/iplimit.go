package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vyto4ka/vynel/internal/node/firewall"
	"github.com/vyto4ka/vynel/internal/node/state"
	nodev1 "github.com/vyto4ka/vynel/internal/proto/vynel/node/v1"
	"github.com/vyto4ka/vynel/internal/xrayconf"
)

// CapIPLimit is the capability of agents that report who is online from where (OnlineIPs)
// and apply IPBlocks (docs/USER_GUIDE.md §6.1).
const CapIPLimit = "iplimit"

// onlineIPs reads who is connected now and from which addresses (nil when Xray is not up).
func (a *Agent) onlineIPs(ctx context.Context) *nodev1.OnlineIPs {
	a.mu.Lock()
	api := a.api
	running := a.proc.Running()
	a.mu.Unlock()
	if api == nil || !running {
		return nil
	}
	cctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	online, err := api.OnlineUsers(cctx)
	if err != nil {
		a.log.Debug("cannot read online users", "err", err)
		return nil
	}
	msg := &nodev1.OnlineIPs{}
	for _, o := range online {
		ips := append([]string(nil), o.IPList...)
		sort.Strings(ips)
		msg.Users = append(msg.Users, &nodev1.OnlineUser{Email: o.Email, Ips: int32(len(ips)), IpList: ips})
	}
	sort.Slice(msg.Users, func(i, j int) bool { return msg.Users[i].Email < msg.Users[j].Email })
	return msg
}

// SetIPBlocks takes the panel's full list of blocked addresses and applies it.
func (a *Agent) SetIPBlocks(ctx context.Context, blocks []*nodev1.IPBlock) {
	a.mu.Lock()
	defer a.mu.Unlock()
	prev := a.blockedIPs()
	a.ipBlocks = blocks
	if err := a.applyBlocksLocked(ctx, a.cur); err != nil {
		a.log.Error("cannot apply address blocks", "err", err)
		return
	}
	var fresh []string
	for ip := range a.blockedIPs() {
		if !prev[ip] {
			fresh = append(fresh, ip)
		}
	}
	if len(fresh) > 0 {
		sort.Strings(fresh)
		a.log.Info("blocked addresses over users' limits", "addrs", fresh)
		a.dropConnections(ctx, fresh)
	}
}

func (a *Agent) blockedIPs() map[string]bool {
	m := map[string]bool{}
	for _, b := range a.ipBlocks {
		for _, ip := range b.Ips {
			m[ip] = true
		}
	}
	return m
}

// blockRules are the routing rules of the blocks: a user's emails ("<id>", "<id>.…") from the
// listed addresses go to the blackhole.
func blockRules(blocks []*nodev1.IPBlock) []any {
	var rules []any
	for _, b := range blocks {
		if len(b.Ips) == 0 {
			continue
		}
		id := strconv.FormatInt(b.UserId, 10)
		rules = append(rules, map[string]any{
			"ruleTag": "vynel-ipblock-" + id, "user": []string{"regexp:^" + id + `(\..*)?$`},
			"source": b.Ips, "outboundTag": xrayconf.IPBlockTag,
		})
	}
	return rules
}

// applyBlocksLocked puts the block rules in front of the config's own routing rules in the
// running Xray. Rules added through the API can only go last, behind the config's catch-all
// rule, so the whole list is replaced: blocks first, then the config's rules as they are.
func (a *Agent) applyBlocksLocked(ctx context.Context, st *state.State) error {
	rules := blockRules(a.ipBlocks)
	if len(rules) == 0 && !a.blocksApplied {
		return nil // nothing blocked, nothing to undo
	}
	if st == nil || a.api == nil || !a.proc.Running() {
		return nil // applied after the next start
	}
	var cfg map[string]any
	if err := json.Unmarshal(st.Config, &cfg); err != nil {
		return err
	}
	outbounds, _ := cfg["outbounds"].([]any)
	hasBlock := slices.ContainsFunc(outbounds, func(o any) bool {
		m, _ := o.(map[string]any)
		return m["tag"] == xrayconf.IPBlockTag
	})
	if !hasBlock && len(rules) > 0 {
		return fmt.Errorf("the config has no %s outbound (the panel is older than this node)", xrayconf.IPBlockTag)
	}
	routing := map[string]any{}
	if r, ok := cfg["routing"].(map[string]any); ok {
		for k, v := range r {
			routing[k] = v
		}
	}
	own, _ := routing["rules"].([]any)
	routing["rules"] = append(rules, own...)
	if err := a.cfg.Xray.SetRules(ctx, a.apiAddr, routing); err != nil {
		return err
	}
	a.blocksApplied = len(rules) > 0
	return nil
}

// dropConnections closes the open TCP connections of blocked addresses on the ports this node
// serves, so they do not keep going: new ones meet the block rule. UDP (Hysteria) has no
// per-client socket to close; its new streams meet the rule.
func (a *Agent) dropConnections(ctx context.Context, addrs []string) {
	var ports []int
	for _, p := range a.servedPortsLocked() {
		if p.Proto == "tcp" && !p.Restricted && len(p.From) == 0 && !slices.Contains(ports, p.Port) {
			ports = append(ports, p.Port)
		}
	}
	if len(ports) == 0 {
		return
	}
	var sport []string
	for _, p := range ports {
		sport = append(sport, "sport = :"+strconv.Itoa(p))
	}
	for _, ip := range addrs {
		args := []string{"-K", "-t", "-n", "dst", ip, "(", strings.Join(sport, " or "), ")"}
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		out, err := exec.CommandContext(cctx, "ss", args...).CombinedOutput()
		cancel()
		if err != nil {
			a.log.Warn("cannot close connections of a blocked address", "addr", ip, "err", err, "out", strings.TrimSpace(string(out)))
		}
	}
}

// servedPortsLocked is FirewallPorts without the lock (and without the extras: the panel's own
// gateway is not a proxy port).
func (a *Agent) servedPortsLocked() []firewall.Port {
	var ports []firewall.Port
	if a.cur != nil {
		ports = append(ports, firewall.XrayPorts(a.cur.Config)...)
	}
	ports = append(ports, firewall.CaddyPorts(a.caddyCfg)...)
	return append(ports, firewall.NginxPorts(a.nginxCfg)...)
}
