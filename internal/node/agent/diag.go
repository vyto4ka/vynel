package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vyto4ka/vynel/internal/xray"
)

// Diagnose writes a troubleshooting report for this node: versions, what Xray and Caddy run,
// sockets, certificates, the local Reality target, DNS, firewall and the agent's journal.
// It never prints keys or user ids, so the report can be shared.
func Diagnose(ctx context.Context, w io.Writer, dataDir, version string, bin xray.Binary, caddyBin string) {
	section := func(t string) { fmt.Fprintf(w, "\n===== %s =====\n", t) }
	run := func(name string, args ...string) {
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		out, err := exec.CommandContext(cctx, name, args...).CombinedOutput()
		fmt.Fprintf(w, "$ %s %s\n%s", name, strings.Join(args, " "), out)
		if err != nil {
			fmt.Fprintf(w, "(%v)\n", err)
		}
	}

	section("versions")
	fmt.Fprintf(w, "vynel %s\n", version)
	if v, err := bin.Version(ctx); err == nil {
		fmt.Fprintf(w, "xray %s\n", v)
	} else {
		fmt.Fprintf(w, "xray: %v\n", err)
	}
	run(caddyBin, "version")
	fmt.Fprintf(w, "time %s (Reality needs the clock within ~1 minute of the client's)\n", time.Now().UTC().Format(time.RFC3339))

	section("inbounds (xray.json)")
	raw, err := os.ReadFile(filepath.Join(dataDir, "xray.json"))
	var cfg map[string]any
	if err == nil {
		err = json.Unmarshal(raw, &cfg)
	}
	if err != nil {
		fmt.Fprintf(w, "cannot read the running config: %v\n", err)
	}
	var domains, targets []string
	ins, _ := cfg["inbounds"].([]any)
	for _, x := range ins {
		in, _ := x.(map[string]any)
		ss, _ := in["streamSettings"].(map[string]any)
		settings, _ := in["settings"].(map[string]any)
		clients, _ := settings["clients"].([]any)
		fmt.Fprintf(w, "- %v: %v on %v:%v, network=%v security=%v users=%d\n", in["tag"], in["protocol"], in["listen"], in["port"],
			ss["network"], ss["security"], len(clients))
		if rs, ok := ss["realitySettings"].(map[string]any); ok {
			fmt.Fprintf(w, "    reality target=%v serverNames=%v shortIds=%v\n", rs["target"], rs["serverNames"], rs["shortIds"])
			if t, _ := rs["target"].(string); t != "" {
				for _, sn := range anyStrings(rs["serverNames"]) {
					targets = append(targets, t+"|"+sn)
					domains = append(domains, sn)
				}
			}
		}
		if xs, ok := ss["xhttpSettings"].(map[string]any); ok {
			fmt.Fprintf(w, "    xhttp path=%v mode=%v\n", xs["path"], xs["mode"])
		}
		if ts, ok := ss["tlsSettings"].(map[string]any); ok {
			for _, c := range anySlice(ts["certificates"]) {
				cm, _ := c.(map[string]any)
				fmt.Fprintf(w, "    certificate %v\n", cm["certificateFile"])
				if f, _ := cm["certificateFile"].(string); f != "" {
					fmt.Fprintf(w, "      %s\n", describeCert(f))
				}
			}
		}
	}
	if raw != nil {
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		if err := bin.Test(cctx, raw); err != nil {
			fmt.Fprintf(w, "xray -test: %v\n", err)
		} else {
			fmt.Fprintln(w, "xray -test: OK")
		}
		cancel()
	}

	section("certificates from Caddy")
	matches, _ := filepath.Glob(filepath.Join(dataDir, "web", "caddy", "caddy", "certificates", "*", "*", "*.crt"))
	if len(matches) == 0 {
		fmt.Fprintln(w, "none yet: Caddy has not obtained any certificate (DNS A record? port 80 reachable from the internet?)")
	}
	for _, m := range matches {
		fmt.Fprintf(w, "- %s\n  %s\n", strings.TrimPrefix(m, dataDir+"/"), describeCert(m))
	}

	section("Reality targets (the local Caddy site)")
	for _, t := range dedupe(targets) {
		addr, sni, _ := strings.Cut(t, "|")
		fmt.Fprintf(w, "- %s SNI %s: %s\n", addr, sni, probeTLS(addr, sni))
	}

	section("DNS")
	for _, d := range dedupe(domains) {
		ips, err := net.DefaultResolver.LookupHost(ctx, d)
		fmt.Fprintf(w, "- %s -> %v %v\n", d, ips, errString(err))
	}
	fmt.Fprintf(w, "this server's addresses: %v\n", localIPs())

	section("listening sockets")
	run("ss", "-ltnup")

	section("firewall")
	run("ufw", "status")

	section("agent journal (last 300 lines)")
	run("journalctl", "-u", "vynel-node", "-n", "300", "--no-pager", "-o", "short-iso")
}

func describeCert(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err.Error()
	}
	blk, _ := pem.Decode(raw)
	if blk == nil {
		return "not PEM"
	}
	c, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		return err.Error()
	}
	kind := ""
	if len(c.Subject.Organization) > 0 && c.Subject.Organization[0] == selfSignedOrg {
		kind = " [TEMPORARY self-signed: Caddy has no certificate for this domain yet]"
	}
	return fmt.Sprintf("names=%v issuer=%q valid until %s%s", c.DNSNames, c.Issuer.CommonName, c.NotAfter.UTC().Format("2006-01-02"), kind)
}

// probeTLS connects like Reality does: TLS 1.3 with h2 to the target with the client's SNI.
func probeTLS(addr, sni string) string {
	d := &net.Dialer{Timeout: 5 * time.Second}
	conn, err := tls.DialWithDialer(d, "tcp", addr, &tls.Config{ServerName: sni, NextProtos: []string{"h2", "http/1.1"},
		MinVersion: tls.VersionTLS13, InsecureSkipVerify: true}) //nolint:gosec // only reporting what the target serves
	if err != nil {
		return "FAILED: " + err.Error() + " (Reality cannot work without its target)"
	}
	defer func() { _ = conn.Close() }()
	st := conn.ConnectionState()
	c := st.PeerCertificates[0]
	return fmt.Sprintf("OK, ALPN=%q, certificate names=%v issuer=%q", st.NegotiatedProtocol, c.DNSNames, c.Issuer.CommonName)
}

func localIPs() []string {
	var out []string
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.IsGlobalUnicast() {
			out = append(out, n.IP.String())
		}
	}
	return out
}

func anySlice(v any) []any { s, _ := v.([]any); return s }

func anyStrings(v any) []string {
	var out []string
	for _, x := range anySlice(v) {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func dedupe(s []string) []string {
	m := map[string]bool{}
	var out []string
	for _, x := range s {
		if !m[x] {
			m[x] = true
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}

func errString(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}
