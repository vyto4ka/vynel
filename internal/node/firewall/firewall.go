// Package firewall keeps an nftables table that lets in only what this server really serves:
// SSH, the Xray inbounds, the Caddy sites and the node gateway. Everything else is dropped
// silently, and an address that knocks on many closed ports is taken for a scanner and dropped
// entirely for a day, so the server looks like a plain web server with nothing else on it
// (docs/INSTALL_GUIDE.md §7).
//
// The table is built from the configs the node already runs, so a new inbound on another port
// opens that port by itself. It is switched on and off with `vynel firewall on|off`; the state
// lives in a small file shared by the panel and the node on the same server.
package firewall

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Table is the nftables table vynel owns; nothing else is touched.
const Table = "vynel"

// DefaultConfPath is the on/off switch and the extra ports.
const DefaultConfPath = "/etc/vynel/firewall.json"

// ConfPath can be moved for tests (VYNEL_FIREWALL_CONF).
func ConfPath() string {
	if p := os.Getenv("VYNEL_FIREWALL_CONF"); p != "" {
		return p
	}
	return DefaultConfPath
}

// Port is something to let in.
type Port struct {
	Port  int    `json:"port"`
	Proto string `json:"proto"` // tcp | udp
	Why   string `json:"why,omitempty"`
}

func (p Port) String() string { return strconv.Itoa(p.Port) + "/" + p.Proto }

// Conf is what the admin decides (firewall.json).
type Conf struct {
	Enabled bool     `json:"enabled"`
	Allow   []string `json:"allow,omitempty"`   // extra ports: "8080", "5000/udp"
	NoTrap  bool     `json:"noTrap,omitempty"`  // do not ban port scanners
	NoPing  bool     `json:"noPing,omitempty"`  // do not answer ping
	SSHRate int      `json:"sshRate,omitempty"` // new SSH connections per minute per address, 0 = 10
}

// LoadConf reads the switch; a missing file means "off".
func LoadConf() (Conf, error) {
	var c Conf
	b, err := os.ReadFile(ConfPath())
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	return c, json.Unmarshal(b, &c)
}

// SaveConf writes the switch.
func SaveConf(c Conf) error {
	if err := os.MkdirAll(filepath.Dir(ConfPath()), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(ConfPath(), append(b, '\n'), 0o644)
}

// ParsePort reads "443", "443/udp".
func ParsePort(s string) (Port, error) {
	num, proto, _ := strings.Cut(strings.TrimSpace(s), "/")
	if proto == "" {
		proto = "tcp"
	}
	n, err := strconv.Atoi(num)
	if err != nil || n < 1 || n > 65535 || (proto != "tcp" && proto != "udp") {
		return Port{}, fmt.Errorf("%q: expected PORT or PORT/udp", s)
	}
	return Port{Port: n, Proto: proto, Why: "allowed by hand"}, nil
}

// ---- what the server serves ----

func public(host string) bool {
	if host == "" || host == "0.0.0.0" || host == "::" {
		return true
	}
	ip := net.ParseIP(host)
	return ip == nil || !ip.IsLoopback()
}

func portOf(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	case string:
		n, _ := strconv.Atoi(x)
		return n
	}
	return 0
}

// XrayPorts lists the public inbounds of an Xray config. Hysteria (QUIC) is UDP, the rest TCP.
func XrayPorts(cfg []byte) []Port {
	var c struct {
		Inbounds []struct {
			Tag            string         `json:"tag"`
			Listen         string         `json:"listen"`
			Port           any            `json:"port"`
			Protocol       string         `json:"protocol"`
			StreamSettings map[string]any `json:"streamSettings"`
		} `json:"inbounds"`
	}
	if json.Unmarshal(cfg, &c) != nil {
		return nil
	}
	var out []Port
	for _, in := range c.Inbounds {
		p := portOf(in.Port)
		if p == 0 || !public(in.Listen) {
			continue
		}
		proto := "tcp"
		if in.Protocol == "hysteria" || in.StreamSettings["network"] == "hysteria" || in.StreamSettings["network"] == "kcp" {
			proto = "udp"
		}
		out = append(out, Port{Port: p, Proto: proto, Why: "Xray " + in.Tag})
	}
	return out
}

// CaddyPorts lists the public listeners of a Caddy JSON config.
func CaddyPorts(cfg []byte) []Port {
	var c struct {
		Apps struct {
			HTTP struct {
				Servers map[string]struct {
					Listen []string `json:"listen"`
				} `json:"servers"`
			} `json:"http"`
		} `json:"apps"`
	}
	if json.Unmarshal(cfg, &c) != nil {
		return nil
	}
	var out []Port
	for _, s := range c.Apps.HTTP.Servers {
		for _, l := range s.Listen {
			host, port, err := net.SplitHostPort(l)
			if err != nil || !public(host) {
				continue
			}
			if n, _ := strconv.Atoi(port); n > 0 {
				out = append(out, Port{Port: n, Proto: "tcp", Why: "Caddy"})
			}
		}
	}
	return out
}

// ListenPort turns a listen address (":9443", "127.0.0.1:9443") into a public TCP port, if any.
func ListenPort(addr, why string) []Port {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || !public(host) {
		return nil
	}
	n, _ := strconv.Atoi(port)
	if n == 0 {
		return nil
	}
	return []Port{{Port: n, Proto: "tcp", Why: why}}
}

var sshdListen = regexp.MustCompile(`:(\d+)\s`)

// SSHPorts finds the ports SSH listens on: the sshd processes, a socket-activated ssh.socket
// and the sshd config. Port 22 when nothing is found — the firewall never locks the admin out.
func SSHPorts(ctx context.Context) []int {
	set := map[int]bool{}
	if out, err := exec.CommandContext(ctx, "ss", "-Hltnp").Output(); err == nil {
		for _, l := range strings.Split(string(out), "\n") {
			if strings.Contains(l, `"sshd"`) || strings.Contains(l, `"sshd-session"`) {
				for _, m := range sshdListen.FindAllStringSubmatch(l+" ", -1) {
					if n, _ := strconv.Atoi(m[1]); n > 0 {
						set[n] = true
						break
					}
				}
			}
		}
	}
	if out, err := exec.CommandContext(ctx, "systemctl", "show", "ssh.socket", "-p", "Listen", "--value").Output(); err == nil {
		for _, f := range strings.Fields(string(out)) {
			if i := strings.LastIndex(f, ":"); i >= 0 {
				if n, _ := strconv.Atoi(f[i+1:]); n > 0 {
					set[n] = true
				}
			}
		}
	}
	if out, err := exec.CommandContext(ctx, "sshd", "-T").Output(); err == nil {
		for _, l := range strings.Split(string(out), "\n") {
			if f := strings.Fields(l); len(f) == 2 && f[0] == "port" {
				if n, _ := strconv.Atoi(f[1]); n > 0 {
					set[n] = true
				}
			}
		}
	}
	if len(set) == 0 {
		set[22] = true
	}
	out := make([]int, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Ints(out)
	return out
}

// ---- the ruleset ----

// Spec is everything Render needs.
type Spec struct {
	SSH   []int
	Ports []Port
	Conf  Conf
}

func dedupe(ps []Port) []Port {
	seen := map[string]Port{}
	for _, p := range ps {
		k := p.String()
		if old, ok := seen[k]; ok && old.Why != "" {
			continue
		}
		seen[k] = p
	}
	out := make([]Port, 0, len(seen))
	for _, p := range seen {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Port != out[j].Port {
			return out[i].Port < out[j].Port
		}
		return out[i].Proto < out[j].Proto
	})
	return out
}

func portSet(ps []Port, proto string, ssh []int) string {
	var ns []string
	for _, p := range ps {
		if p.Proto == proto && !slices.Contains(ssh, p.Port) {
			ns = append(ns, strconv.Itoa(p.Port))
		}
	}
	if len(ns) == 0 {
		return ""
	}
	return "{ " + strings.Join(ns, ", ") + " }"
}

func ints(ns []int) string {
	s := make([]string, len(ns))
	for i, n := range ns {
		s[i] = strconv.Itoa(n)
	}
	return "{ " + strings.Join(s, ", ") + " }"
}

// Render builds the nft script. It replaces the whole table in one transaction, so there is no
// moment without rules, and established connections (your SSH session) are never cut.
func Render(s Spec) string {
	ports := dedupe(s.Ports)
	rate := s.Conf.SSHRate
	if rate <= 0 {
		rate = 10
	}
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	w("# managed by vynel: `vynel firewall status`, emergency off: `vynel firewall off`")
	w("table inet %s {}", Table)
	w("delete table inet %s", Table)
	w("table inet %s {", Table)
	w("  set scanners4 { type ipv4_addr; flags timeout; timeout 1d; }")
	w("  set scanners6 { type ipv6_addr; flags timeout; timeout 1d; }")
	w("  chain input {")
	w("    type filter hook input priority filter - 5; policy drop;")
	w(`    iif "lo" accept`)
	w("    ct state established,related accept")
	w("    ct state invalid drop")
	w("    ip saddr @scanners4 drop")
	w("    ip6 saddr @scanners6 drop")
	// ICMP: what a normal server answers. Ping is rate-limited; timestamps and the rest are not
	// answered (they fingerprint the host). IPv6 needs neighbour discovery to work at all.
	if !s.Conf.NoPing {
		w("    icmp type echo-request limit rate 5/second burst 10 packets accept")
		w("    icmpv6 type echo-request limit rate 5/second burst 10 packets accept")
	}
	w("    icmp type { destination-unreachable, time-exceeded, parameter-problem } accept")
	w("    icmpv6 type { destination-unreachable, packet-too-big, time-exceeded, parameter-problem, nd-router-advert, nd-neighbor-solicit, nd-neighbor-advert } accept")
	w("    ip6 saddr fe80::/10 udp dport 546 accept")
	// SSH stays open on every port it listens on; new connections are rate-limited per address.
	ssh := ints(s.SSH)
	w("    tcp dport %s ct state new meter ssh4 size 65535 { ip saddr limit rate over %d/minute burst %d packets } drop", ssh, rate, rate)
	w("    tcp dport %s ct state new meter ssh6 size 65535 { ip6 saddr limit rate over %d/minute burst %d packets } drop", ssh, rate, rate)
	w("    tcp dport %s accept", ssh)
	if set := portSet(ports, "tcp", s.SSH); set != "" {
		w("    tcp dport %s accept", set)
	}
	if set := portSet(ports, "udp", nil); set != "" {
		w("    udp dport %s accept", set)
	}
	// Port scanners: many new TCP connections to closed ports in a minute -> silence for a day.
	if !s.Conf.NoTrap {
		w("    tcp flags & (syn | ack) == syn meter trap4 size 65535 { ip saddr limit rate over 20/minute burst 20 packets } add @scanners4 { ip saddr } drop")
		w("    tcp flags & (syn | ack) == syn meter trap6 size 65535 { ip6 saddr limit rate over 20/minute burst 20 packets } add @scanners6 { ip6 saddr } drop")
	}
	w("  }")
	w("}")
	return b.String()
}

// ---- applying ----

// Runner runs nft; tests replace it.
var Runner = defaultRunner

func defaultRunner(ctx context.Context, stdin string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "nft", args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	return cmd.CombinedOutput()
}

// Remove deletes the table (no-op when there is none).
func Remove(ctx context.Context) error {
	if _, err := Runner(ctx, "", "list", "table", "inet", Table); err != nil {
		return nil
	}
	out, err := Runner(ctx, "", "delete", "table", "inet", Table)
	if err != nil {
		return fmt.Errorf("nft delete table: %w: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

// Present reports whether the table is loaded.
func Present(ctx context.Context) bool {
	_, err := Runner(ctx, "", "list", "table", "inet", Table)
	return err == nil
}

// AppliedPath keeps the last applied ruleset for `vynel firewall status`.
func AppliedPath() string { return strings.TrimSuffix(ConfPath(), ".json") + ".applied.nft" }

// Manager keeps the table in step with the configs it is given and with firewall.json.
type Manager struct {
	Log *slog.Logger

	mu      sync.Mutex
	applied string // last script loaded
	broken  string // last error, logged once
}

// Sync applies the rules for these ports, or removes the table when the firewall is off. It is
// cheap to call often: nothing runs while the rules and the table are unchanged.
func (m *Manager) Sync(ctx context.Context, ports []Port) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	log := m.Log
	if log == nil {
		log = slog.Default()
	}
	conf, err := LoadConf()
	if err != nil {
		return fmt.Errorf("firewall: %s: %w", ConfPath(), err)
	}
	if !conf.Enabled {
		if m.applied != "" || Present(ctx) {
			if err := Remove(ctx); err != nil {
				return err
			}
			log.Info("firewall off: table removed")
		}
		m.applied = ""
		return nil
	}
	for _, a := range conf.Allow {
		p, err := ParsePort(a)
		if err != nil {
			log.Warn("firewall: bad allow entry", "entry", a, "err", err)
			continue
		}
		ports = append(ports, p)
	}
	script := Render(Spec{SSH: SSHPorts(ctx), Ports: ports, Conf: conf})
	if script == m.applied && Present(ctx) {
		return nil
	}
	out, err := Runner(ctx, script, "-f", "-")
	if err != nil {
		e := fmt.Sprintf("nft: %v: %s", err, bytes.TrimSpace(out))
		if e != m.broken {
			log.Error("firewall rules not applied", "err", e)
			m.broken = e
		}
		return fmt.Errorf("firewall: %s", e)
	}
	m.broken = ""
	m.applied = script
	_ = os.WriteFile(AppliedPath(), []byte(script), 0o644)
	var open []string
	for _, p := range dedupe(ports) {
		open = append(open, p.String())
	}
	log.Info("firewall rules applied", "ssh", SSHPortsString(script), "open", strings.Join(open, " "))
	return nil
}

// SSHPortsString pulls the SSH ports out of a rendered script (for logs and status).
func SSHPortsString(script string) string {
	for _, l := range strings.Split(script, "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "tcp dport {") && strings.HasSuffix(l, "} accept") && !strings.Contains(l, "meter") {
			return strings.TrimSuffix(strings.TrimPrefix(l, "tcp dport "), " accept")
		}
	}
	return ""
}

// Loop re-syncs every interval: it notices `vynel firewall on|off|allow` and a table flushed by
// someone else. ports is called each time for the current configs.
func (m *Manager) Loop(ctx context.Context, every time.Duration, ports func() []Port) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		_ = m.Sync(ctx, ports())
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
