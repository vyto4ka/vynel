package service

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/vyto4ka/vynel/internal/buildinfo"
	"github.com/vyto4ka/vynel/internal/panel/store"
)

// processStart is when this process began: the panel host's uptime.
var processStart = time.Now()

// SelfCert is the state of one certificate of the panel's own domains.
type SelfCert struct {
	Domain      string `json:"domain"`
	Issuer      string `json:"issuer"` // e.g. Let's Encrypt; "internal" = the local CA
	NotAfter    int64  `json:"notAfter"`
	Placeholder bool   `json:"placeholder"` // self-signed until the real one is issued
	OK          bool   `json:"ok"`          // a certificate is there and readable
}

// SelfHost describes the server the panel itself runs on — the card above the node list.
type SelfHost struct {
	Domains  []string   `json:"domains"`  // subscription and panel domains from the settings
	Version  string     `json:"version"`  // panel build
	Uptime   int64      `json:"uptime"`   // seconds since the process started
	WithNode bool       `json:"withNode"` // the local node runs inside this process
	Certs    []SelfCert `json:"certs"`
}

// SelfHost collects what the panel knows about its own server. It never creates a node
// record: a panel installed without a node stays without one.
func (s *Service) SelfHost(ctx context.Context) (*SelfHost, error) {
	out := &SelfHost{Version: buildinfo.Version, Uptime: int64(time.Since(processStart).Seconds())}
	for _, key := range []string{SettingSubDomain, SettingWebDomain} {
		d, err := s.Setting(ctx, key, "")
		if err != nil {
			return nil, err
		}
		d = strings.ToLower(strings.TrimSpace(d))
		if d != "" && !slices.Contains(out.Domains, d) {
			out.Domains = append(out.Domains, d)
		}
	}
	nodes, err := store.ListNodes(ctx, s.st.DB)
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		if n.Local {
			out.WithNode = true
			break
		}
	}
	out.Certs = []SelfCert{}
	for _, d := range out.Domains {
		c := SelfCert{Domain: d}
		if s.SelfCertStatus != nil {
			if st, ok := s.SelfCertStatus(d); ok {
				c = SelfCert{Domain: d, Issuer: st.Issuer, NotAfter: st.NotAfter.Unix(), Placeholder: st.Placeholder, OK: true}
			}
		}
		out.Certs = append(out.Certs, c)
	}
	return out, nil
}
