package firewall

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestPortsFromConfigs(t *testing.T) {
	xray := []byte(`{"inbounds":[
		{"tag":"api","listen":"127.0.0.1","port":10085,"protocol":"dokodemo-door"},
		{"tag":"VLESS_NL","listen":"0.0.0.0","port":443,"protocol":"vless","streamSettings":{"network":"tcp"}},
		{"tag":"HY_NL","port":443,"protocol":"hysteria","streamSettings":{"network":"hysteria"}},
		{"tag":"XHTTP_NL","listen":"203.0.113.5","port":"8443","protocol":"vless","streamSettings":{"network":"xhttp"}}]}`)
	got := map[string]bool{}
	for _, p := range XrayPorts(xray) {
		got[p.String()] = true
	}
	if len(got) != 3 || !got["443/tcp"] || !got["443/udp"] || !got["8443/tcp"] {
		t.Fatalf("xray ports %v", got)
	}
	caddy := []byte(`{"apps":{"http":{"servers":{"https0":{"listen":["127.0.0.1:8443"]},"https1":{"listen":[":47321"]},"http":{"listen":[":80"]}}}}}`)
	got = map[string]bool{}
	for _, p := range CaddyPorts(caddy) {
		got[p.String()] = true
	}
	if len(got) != 2 || !got["47321/tcp"] || !got["80/tcp"] {
		t.Fatalf("caddy ports %v", got)
	}
	if len(ListenPort("127.0.0.1:9443", "gw")) != 0 || ListenPort(":9443", "gw")[0].Port != 9443 {
		t.Fatal("gateway port")
	}
}

func TestRender(t *testing.T) {
	s := Render(Spec{SSH: []int{2222}, Ports: []Port{{443, "tcp", "a"}, {443, "tcp", "b"}, {443, "udp", ""}, {2222, "tcp", ""}}})
	for _, want := range []string{"policy drop", "tcp dport { 2222 } accept", "tcp dport { 443 } accept", "udp dport { 443 } accept", "@scanners4", "echo-request"} {
		if !strings.Contains(s, want) {
			t.Errorf("no %q in\n%s", want, s)
		}
	}
	if SSHPortsString(s) != "{ 2222 }" {
		t.Errorf("ssh %q", SSHPortsString(s))
	}
	quiet := Render(Spec{SSH: []int{22}, Conf: Conf{NoTrap: true, NoPing: true}})
	if strings.Contains(quiet, "add @scanners4") || strings.Contains(quiet, "echo-request") {
		t.Errorf("trap or ping left in\n%s", quiet)
	}
}

func TestSync(t *testing.T) {
	t.Setenv("VYNEL_FIREWALL_CONF", filepath.Join(t.TempDir(), "firewall.json"))
	loaded := ""
	var calls []string
	Runner = func(_ context.Context, stdin string, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		switch {
		case args[0] == "-f":
			loaded = stdin
		case args[0] == "list" && loaded == "":
			return nil, errors.New("no table")
		case args[0] == "delete":
			loaded = ""
		}
		return nil, nil
	}
	defer func() { Runner = defaultRunner }()
	m := &Manager{}
	ctx := context.Background()
	ports := []Port{{443, "tcp", "Caddy"}}

	// Off (no file): nothing loaded.
	if err := m.Sync(ctx, ports); err != nil || loaded != "" {
		t.Fatalf("off: %v %q", err, loaded)
	}
	if err := SaveConf(Conf{Enabled: true, Allow: []string{"5000/udp"}}); err != nil {
		t.Fatal(err)
	}
	if err := m.Sync(ctx, ports); err != nil || !strings.Contains(loaded, "udp dport { 5000 } accept") {
		t.Fatalf("on: %v\n%s", err, loaded)
	}
	n := len(calls)
	if err := m.Sync(ctx, ports); err != nil || len(calls) != n+1 { // only the presence check
		t.Fatalf("unchanged rules reloaded: %v", calls[n:])
	}
	if err := SaveConf(Conf{}); err != nil {
		t.Fatal(err)
	}
	if err := m.Sync(ctx, ports); err != nil || loaded != "" {
		t.Fatalf("off again: %v %q", err, loaded)
	}
}
