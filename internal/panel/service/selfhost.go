package service

import (
	"context"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/vyto4ka/vynel/internal/buildinfo"
	"github.com/vyto4ka/vynel/internal/certs"
	"github.com/vyto4ka/vynel/internal/panel/store"
	nodev1 "github.com/vyto4ka/vynel/internal/proto/vynel/node/v1"
	"github.com/vyto4ka/vynel/internal/xrayconf"
)

// processStart is when this process began: the panel host's uptime.
var processStart = time.Now()

// SelfCert is the state of one TLS certificate (used both for the panel host and for nodes).
type SelfCert struct {
	Domain      string   `json:"domain"`
	Issuer      string   `json:"issuer"` // e.g. Let's Encrypt; "internal" = the local CA
	NotAfter    int64    `json:"notAfter"`
	DaysLeft    int      `json:"daysLeft"`
	Placeholder bool     `json:"placeholder"` // self-signed until the real one is issued
	OK          bool     `json:"ok"`          // a certificate is there and readable
	LastError   string   `json:"lastError,omitempty"`
	RetryAt     int64    `json:"retryAt,omitempty"`
	Roles       []string `json:"roles,omitempty"` // "panel", "sub", inbound tag, "node"
}

// SelfHost describes the server the panel itself runs on — the card above the node list.
type SelfHost struct {
	Domains        []string   `json:"domains"`  // subscription and panel domains from the settings
	Version        string     `json:"version"`  // panel build
	Uptime         int64      `json:"uptime"`   // seconds since the process started
	WithNode       bool       `json:"withNode"` // the local node runs inside this process
	RenewSupported bool       `json:"renewSupported"`
	Certs          []SelfCert `json:"certs"`
}

func certDaysLeft(now time.Time, notAfter int64) int {
	if notAfter <= 0 {
		return 0
	}
	return int(math.Floor(time.Unix(notAfter, 0).Sub(now).Hours() / 24))
}

func selfCertFromStatus(now time.Time, domain string, st certs.Status, roles []string) SelfCert {
	notAfter := int64(0)
	if !st.NotAfter.IsZero() {
		notAfter = st.NotAfter.Unix()
	}
	retryAt := int64(0)
	if !st.RetryAt.IsZero() {
		retryAt = st.RetryAt.Unix()
	}
	return SelfCert{
		Domain:      domain,
		Issuer:      st.Issuer,
		NotAfter:    notAfter,
		DaysLeft:    certDaysLeft(now, notAfter),
		Placeholder: st.Placeholder,
		OK:          true,
		LastError:   st.LastError,
		RetryAt:     retryAt,
		Roles:       roles,
	}
}

func selfCertFromProto(now time.Time, c *nodev1.CertInfo, roles []string) SelfCert {
	return SelfCert{
		Domain:      c.Domain,
		Issuer:      c.Issuer,
		NotAfter:    c.NotAfter,
		DaysLeft:    certDaysLeft(now, c.NotAfter),
		Placeholder: c.Placeholder,
		OK:          c.Ok,
		LastError:   c.LastError,
		RetryAt:     c.RetryAt,
		Roles:       roles,
	}
}

// SelfHost collects what the panel knows about its own server. It never creates a node
// record: a panel installed without a node stays without one.
func (s *Service) SelfHost(ctx context.Context) (*SelfHost, error) {
	out := &SelfHost{
		Version:        buildinfo.Version,
		Uptime:         int64(time.Since(processStart).Seconds()),
		RenewSupported: s.SelfCertRenew != nil,
	}
	rolesByDomain := map[string][]string{}
	addRole := func(d, role string) {
		d = strings.ToLower(strings.TrimSpace(d))
		if d == "" {
			return
		}
		if !slices.Contains(out.Domains, d) {
			out.Domains = append(out.Domains, d)
		}
		if !slices.Contains(rolesByDomain[d], role) {
			rolesByDomain[d] = append(rolesByDomain[d], role)
		}
	}
	for _, pair := range []struct{ key, role string }{
		{SettingSubDomain, "sub"},
		{SettingWebDomain, "panel"},
	} {
		d, err := s.Setting(ctx, pair.key, "")
		if err != nil {
			return nil, err
		}
		addRole(d, pair.role)
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
	now := s.now()
	out.Certs = []SelfCert{}
	for _, d := range out.Domains {
		c := SelfCert{Domain: d, Roles: rolesByDomain[d]}
		if s.SelfCertStatus != nil {
			if st, ok := s.SelfCertStatus(d); ok {
				c = selfCertFromStatus(now, d, st, rolesByDomain[d])
			}
		}
		out.Certs = append(out.Certs, c)
	}
	return out, nil
}

// RenewSelfCerts force-renews TLS certificates on the panel host (empty domain = all).
func (s *Service) RenewSelfCerts(ctx context.Context, actor Actor, domain string) ([]string, error) {
	if s.SelfCertRenew == nil {
		return nil, invalid("управление сертификатами на этом сервере недоступно")
	}
	domain = strings.ToLower(strings.TrimSpace(domain))
	renewed, err := s.SelfCertRenew(ctx, domain)
	res := "ok"
	if err != nil {
		res = err.Error()
	} else if len(renewed) > 0 {
		res = strings.Join(renewed, ", ")
	}
	_ = s.LogPanelAction(ctx, actor, "certs", res)
	return renewed, err
}

// NodeCerts returns all TLS certificates relevant to a node, combining its expected domains
// (panel/sub domains when Local, inbound web domains, node domain) with the live statuses
// reported by the node agent.
func (s *Service) NodeCerts(ctx context.Context, n *store.Node, reported []*nodev1.CertInfo) []SelfCert {
	rolesByDomain := map[string][]string{}
	addRole := func(d, role string) {
		d = strings.ToLower(strings.TrimSpace(d))
		if d == "" {
			return
		}
		if role != "" && !slices.Contains(rolesByDomain[d], role) {
			rolesByDomain[d] = append(rolesByDomain[d], role)
		} else if _, ok := rolesByDomain[d]; !ok {
			rolesByDomain[d] = nil
		}
	}

	// Panel & subscription domains live on the local node when AIO mode is enabled.
	if n.Local {
		if d, err := s.Setting(ctx, SettingWebDomain, ""); err == nil {
			addRole(d, "panel")
		}
		if d, err := s.Setting(ctx, SettingSubDomain, ""); err == nil {
			addRole(d, "sub")
		}
	}

	// Inbound web domains (selfsteal, certificate/Hysteria2, reverse_proxy_xhttp).
	if nis, err := s.NodeInbounds(ctx, n.ID); err == nil {
		for _, ni := range nis {
			if !ni.Enabled {
				continue
			}
			rendered, err := s.RenderNodeInbound(ctx, ni.ID)
			if err != nil || rendered == nil || rendered.Template == nil || rendered.Template.Web() == nil {
				continue
			}
			vals := copyMap(rendered.Values)
			vals["INBOUND_PORT"], _ = rendered.Inbound["port"].(int)
			cm, err := xrayconf.ExpandTree(rendered.Template.Web(), vals)
			if err != nil {
				continue
			}
			switch str(cm["role"]) {
			case "selfsteal", "certificate", "reverse_proxy_xhttp":
				role := ni.Tag
				if role == "" {
					role = "inbound"
				}
				addRole(str(cm["domain"]), role)
				for _, x := range anyList(cm["server_names"]) {
					addRole(str(x), role)
				}
			}
		}
	}

	repByDomain := map[string]*nodev1.CertInfo{}
	for _, c := range reported {
		d := strings.ToLower(strings.TrimSpace(c.Domain))
		if d == "" {
			continue
		}
		repByDomain[d] = c
		if _, ok := rolesByDomain[d]; !ok {
			rolesByDomain[d] = nil
		}
	}

	if nd := strings.ToLower(strings.TrimSpace(n.Domain)); nd != "" {
		if _, exists := rolesByDomain[nd]; !exists && len(repByDomain) > 0 {
			// Only add node.Domain as fallback if it wasn't already covered, or if agent reported it.
			if _, ok := repByDomain[nd]; ok {
				addRole(nd, "node")
			}
		}
	}

	now := s.now()
	out := make([]SelfCert, 0, len(rolesByDomain))
	for d, roles := range rolesByDomain {
		if c, ok := repByDomain[d]; ok {
			out = append(out, selfCertFromProto(now, c, roles))
			continue
		}
		if n.Local && s.SelfCertStatus != nil {
			if st, ok := s.SelfCertStatus(d); ok {
				out = append(out, selfCertFromStatus(now, d, st, roles))
				continue
			}
		}
		out = append(out, SelfCert{Domain: d, Roles: roles})
	}

	// Stable order: panel/sub domains first, then alphabetical.
	prio := func(roles []string) int {
		if slices.Contains(roles, "panel") {
			return 0
		}
		if slices.Contains(roles, "sub") {
			return 1
		}
		return 2
	}
	sort.Slice(out, func(i, j int) bool {
		pi, pj := prio(out[i].Roles), prio(out[j].Roles)
		if pi != pj {
			return pi < pj
		}
		return out[i].Domain < out[j].Domain
	})
	return out
}
