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
	} {
		keep(f.dst, f.src, "")
	}
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
		d := strings.TrimSuffix(cleanDomain(c), ".")
		if seen[d] || !domainRe.MatchString(d) {
			continue
		}
		seen[d] = true
		if CheckDomain(ctx, d, ip).OK {
			return d
		}
	}
	return ""
}
