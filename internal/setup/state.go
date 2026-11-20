package setup

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"
)

// state is what the form keeps between runs: closing the installer half-way (or a failed
// install) and starting it again brings the answers back. scripts/install.sh deletes the file
// once an install succeeds. It may hold the bot token, so it is readable by root only.
type state struct {
	Answers
	Security []string `json:"security"`
	Nodes    *bool    `json:"nodes,omitempty"`
}

func loadState(path string) *state {
	if path == "" {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var s state
	if json.Unmarshal(b, &s) != nil {
		return nil
	}
	return &s
}

func saveState(path string, s *state) {
	if path == "" {
		return
	}
	b, err := json.Marshal(s)
	if err != nil {
		return
	}
	_ = os.WriteFile(path+".tmp", b, 0o600)
	_ = os.Rename(path+".tmp", path)
}

// restore copies saved answers into a, keeping what the command line set (fixed).
func (a *Answers) restore(s *Answers, fixed Options) {
	keep := func(dst *string, src, flag string) {
		if flag == "" && src != "" {
			*dst = src
		}
	}
	keep(&a.Domain, s.Domain, fixed.Domain)
	keep(&a.Email, s.Email, fixed.Email)
	keep(&a.Token, s.Token, fixed.Token)
	for _, f := range []struct {
		dst *string
		src string
	}{
		{&a.SubDomain, s.SubDomain}, {&a.Name, s.Name}, {&a.Country, s.Country}, {&a.PublicIP, s.PublicIP},
		{&a.VPNIP, s.VPNIP}, {&a.SubIP, s.SubIP}, {&a.AdminLogin, s.AdminLogin}, {&a.BotToken, s.BotToken},
		{&a.Restore, s.Restore}, {&a.SubPath, s.SubPath}, {&a.PanelPath, s.PanelPath},
		{&a.LoginMode, s.LoginMode}, {&a.SessionPaths, s.SessionPaths},
	} {
		keep(f.dst, f.src, "")
	}
}

// reservedTLDs are pseudo-TLDs that a server's local hostname often uses (RFC 2606, RFC 6761,
// RFC 6762, ICANN reserved) and that can never receive a public TLS certificate.
var reservedTLDs = map[string]bool{
	"local": true, "localdomain": true, "localhost": true, "internal": true,
	"lan": true, "home": true, "arpa": true, "corp": true, "intranet": true,
	"invalid": true, "test": true, "example": true,
}

// validSystemHostname checks whether a raw hostname returned by the OS (hostname -f,
// os.Hostname, or PTR) is a syntactically valid public DNS hostname (RFC 1035 / RFC 1123)
// suitable as a candidate domain. Unlike cleanDomain (which accepts pasted URLs from user
// input), garbage such as paths, schemes, underscores, overlong labels or local pseudo-TLDs
// is rejected outright.
func validSystemHostname(raw string) (string, bool) {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.TrimSuffix(s, ".")
	if s == "" || len(s) > 253 {
		return "", false
	}
	if net.ParseIP(s) != nil || !domainRe.MatchString(s) {
		return "", false
	}
	labels := strings.Split(s, ".")
	if len(labels) < 2 {
		return "", false
	}
	for _, l := range labels {
		if len(l) == 0 || len(l) > 63 {
			return "", false
		}
	}
	if reservedTLDs[labels[len(labels)-1]] {
		return "", false
	}
	return s, true
}

// DetectDomain looks for a domain this server already answers to: the host name or the reverse
// DNS name of its public IP, if public DNS points it at ip. "" when there is none.
func DetectDomain(ctx context.Context, ip string) string {
	if net.ParseIP(ip) == nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	var cands []string
	if out, err := exec.CommandContext(ctx, "hostname", "-f").Output(); err == nil {
		cands = append(cands, string(out))
	}
	if h, err := os.Hostname(); err == nil {
		cands = append(cands, h)
	}
	if names, err := net.DefaultResolver.LookupAddr(ctx, ip); err == nil {
		cands = append(cands, names...)
	}
	seen := map[string]bool{}
	for _, c := range cands {
		d, ok := validSystemHostname(c)
		if !ok || seen[d] {
			continue
		}
		seen[d] = true
		if CheckDomain(ctx, d, ip).OK {
			return d
		}
	}
	return ""
}
