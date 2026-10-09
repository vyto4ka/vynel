// Package sysctl applies the kernel tuning every node gets (docs/PROFILES.md §6) and reports
// what it could not fix as warnings for the panel.
package sysctl

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Settings is the tuning applied on nodes.
var Settings = []struct{ Key, Value string }{
	{"net.core.default_qdisc", "fq"},
	{"net.ipv4.tcp_congestion_control", "bbr"},
	{"net.ipv4.tcp_fastopen", "3"},
}

// Harden are network safety settings: SYN cookies, no ICMP redirects or source routing, no
// answers to broadcast pings. Applied with the tuning but never reported as warnings: some
// containers do not allow them, and nothing breaks without them.
var Harden = []struct{ Key, Value string }{
	{"net.ipv4.tcp_syncookies", "1"},
	{"net.ipv4.conf.all.accept_redirects", "0"},
	{"net.ipv4.conf.default.accept_redirects", "0"},
	{"net.ipv6.conf.all.accept_redirects", "0"},
	{"net.ipv6.conf.default.accept_redirects", "0"},
	{"net.ipv4.conf.all.send_redirects", "0"},
	{"net.ipv4.conf.all.accept_source_route", "0"},
	{"net.ipv6.conf.all.accept_source_route", "0"},
	{"net.ipv4.icmp_echo_ignore_broadcasts", "1"},
	{"net.ipv4.icmp_ignore_bogus_error_responses", "1"},
}

// ConfPath persists the settings across reboots.
const ConfPath = "/etc/sysctl.d/90-vynel.conf"

func procPath(key string) string {
	return filepath.Join("/proc/sys", strings.ReplaceAll(key, ".", "/"))
}

// Read returns the current value of a sysctl.
func Read(key string) string {
	b, err := os.ReadFile(procPath(key))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Tune applies the settings (needs root) and returns warnings for anything not in place.
func Tune(apply bool) []string {
	if apply {
		if !strings.Contains(Read("net.ipv4.tcp_available_congestion_control"), "bbr") {
			_ = exec.Command("modprobe", "tcp_bbr").Run()
		}
		var conf strings.Builder
		conf.WriteString("# managed by vynel\n")
		for _, s := range append(Settings[:len(Settings):len(Settings)], Harden...) {
			if Read(s.Key) == "" {
				continue // not in this kernel or container
			}
			conf.WriteString(s.Key + " = " + s.Value + "\n")
			_ = os.WriteFile(procPath(s.Key), []byte(s.Value), 0o644)
		}
		_ = os.WriteFile(ConfPath, []byte(conf.String()), 0o644)
	}
	return Check()
}

// Check reports deviations without changing anything.
func Check() []string {
	var warnings []string
	for _, s := range Settings {
		if v := Read(s.Key); v != s.Value {
			warnings = append(warnings, s.Key+"="+orUnknown(v)+" (want "+s.Value+")")
		}
	}
	if out, err := exec.Command("timedatectl", "show", "-p", "NTPSynchronized", "--value").Output(); err == nil && strings.TrimSpace(string(out)) == "no" {
		warnings = append(warnings, "clock is not NTP-synchronized (Reality rejects clients when clocks drift)")
	}
	return warnings
}

func orUnknown(v string) string {
	if v == "" {
		return "?"
	}
	return v
}
