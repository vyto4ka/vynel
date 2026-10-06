package service

import (
	"context"
	"fmt"
	"net"
	"strconv"

	"github.com/vyto4ka/vpn/internal/caddyconf"
	"github.com/vyto4ka/vpn/internal/decoy"
	"github.com/vyto4ka/vpn/internal/panel/store"
	"github.com/vyto4ka/vpn/internal/xrayconf"
)

// Caddy settings.
const (
	SettingCaddyEmail     = "caddy.email"           // ACME account email
	SettingCaddyIssuer    = "caddy.issuer"          // acme (default) | internal (self-signed, tests)
	SettingCaddyHTTPPort  = "caddy.http_port"       // ACME HTTP-01 + redirects, default 80
	SettingCaddyAdminPort = "node.caddy_admin_port" // Caddy admin API on 127.0.0.1, default 2019
	SettingSubAddress     = "sub.address"           // IP the subscription site binds to; empty = all
	SettingCaddyEnabled   = "node.caddy_enabled"    // "false" = the node runs its own web server
)

// realityFront is a Reality inbound that can hide Caddy sites behind it (docs/INBOUNDS.md §2.3).
type realityFront struct {
	tag       string
	listen    string // "0.0.0.0" or an IP
	port      int
	localPort int // Reality target on 127.0.0.1, 0 = target is not local
}

// caddySpec builds the node's Caddy sites from its inbounds and, for the panel's own server,
// the subscription site. Sites that want a public (IP, port) taken by a Reality inbound are
// moved behind it: Reality passes everything that is not a VPN client to its local target.
func (s *Service) caddySpec(ctx context.Context, n *store.Node, inbounds []*xrayconf.RenderedInbound) (caddyconf.Spec, []string, error) {
	var problems []string
	enabled, err := s.nodeSetting(ctx, SettingCaddyEnabled, n.Code, "true")
	if err != nil {
		return caddyconf.Spec{}, nil, err
	}
	if enabled == "false" {
		return caddyconf.Spec{}, nil, nil
	}
	adminPort, err := s.nodeSetting(ctx, SettingCaddyAdminPort, n.Code, "2019")
	if err != nil {
		return caddyconf.Spec{}, nil, err
	}
	spec := caddyconf.Spec{AdminAddr: "127.0.0.1:" + adminPort}
	spec.Email, _ = s.Setting(ctx, SettingCaddyEmail, "")
	spec.Issuer, _ = s.Setting(ctx, SettingCaddyIssuer, "acme")
	if hp, _ := s.Setting(ctx, SettingCaddyHTTPPort, "80"); hp != "" {
		spec.HTTPPort, _ = strconv.Atoi(hp)
	}

	type listener struct{ ip, port string }
	var fronts []realityFront
	xrayListens := map[listener]string{}
	for _, r := range inbounds {
		listen, _ := r.Inbound["listen"].(string)
		if listen == "" {
			listen = "0.0.0.0"
		}
		port, _ := r.Inbound["port"].(int)
		xrayListens[listener{listen, strconv.Itoa(port)}] = r.Tag
		ss, _ := r.Inbound["streamSettings"].(map[string]any)
		if ss["security"] == "reality" {
			rs, _ := ss["realitySettings"].(map[string]any)
			f := realityFront{tag: r.Tag, listen: listen, port: port}
			if host, p, err := net.SplitHostPort(str(rs["target"])); err == nil && (host == "127.0.0.1" || host == "localhost") {
				f.localPort, _ = strconv.Atoi(p)
			}
			fronts = append(fronts, f)
		}
	}

	var sites []caddyconf.Site
	for _, r := range inbounds {
		if r.Template == nil || r.Template.Caddy == nil {
			continue
		}
		vals := copyMap(r.Values)
		vals["INBOUND_PORT"], _ = r.Inbound["port"].(int)
		cm, err := xrayconf.ExpandTree(r.Template.Caddy, vals)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: caddy: %v", r.Tag, err))
			continue
		}
		switch str(cm["role"]) {
		case "selfsteal":
			// Only when this inbound's Reality target really is the local Caddy.
			var front *realityFront
			for i := range fronts {
				if fronts[i].tag == r.Tag {
					front = &fronts[i]
				}
			}
			if front == nil || front.localPort == 0 {
				continue // e.g. a profile override points Reality at a foreign site
			}
			sites = append(sites, caddyconf.Site{Domain: str(cm["domain"]), LocalPort: front.localPort,
				Routes: []caddyconf.Route{{Kind: caddyconf.KindDecoy, Decoy: decoyName(str(cm["decoy"]))}}})
		case "reverse_proxy_xhttp":
			bind := str(vals["LISTEN_IP"])
			if bind == "0.0.0.0" {
				bind = ""
			}
			sites = append(sites, caddyconf.Site{Domain: str(cm["domain"]), Bind: bind, Port: 443, Routes: []caddyconf.Route{
				{Kind: caddyconf.KindStream, Upstream: str(cm["upstream"]), PathPrefix: str(cm["path"])},
				{Kind: caddyconf.KindDecoy, Decoy: "cloud"},
			}})
		}
	}

	if n.Local {
		if site, ok, err := s.subscriptionSite(ctx); err != nil {
			return caddyconf.Spec{}, nil, err
		} else if ok {
			sites = append(sites, site)
		}
	}

	// Place public sites: behind a Reality front on the same (IP, port), or directly.
	for _, site := range sites {
		if site.LocalPort > 0 {
			if tag, ok := xrayListens[listener{"127.0.0.1", strconv.Itoa(site.LocalPort)}]; ok {
				problems = append(problems, fmt.Sprintf("%s: Caddy needs 127.0.0.1:%d but inbound %s listens there", site.Domain, site.LocalPort, tag))
				continue
			}
			if tag, ok := xrayListens[listener{"0.0.0.0", strconv.Itoa(site.LocalPort)}]; ok {
				problems = append(problems, fmt.Sprintf("%s: Caddy needs 127.0.0.1:%d but inbound %s listens on all addresses there", site.Domain, site.LocalPort, tag))
				continue
			}
			spec.Sites = append(spec.Sites, site)
			continue
		}
		var front *realityFront
		for i := range fronts {
			f := &fronts[i]
			if f.port == site.Port && (f.listen == "0.0.0.0" || site.Bind == "" || f.listen == site.Bind) {
				front = f
				break
			}
		}
		if front != nil {
			if front.localPort == 0 {
				problems = append(problems, fmt.Sprintf("%s: port %d is taken by Reality inbound %s whose target is not local, the site cannot hide behind it", site.Domain, site.Port, front.tag))
				continue
			}
			site.LocalPort = front.localPort
			spec.Sites = append(spec.Sites, site)
			continue
		}
		bind := site.Bind
		if bind == "" {
			bind = "0.0.0.0"
		}
		if tag, ok := xrayListens[listener{bind, strconv.Itoa(site.Port)}]; ok {
			problems = append(problems, fmt.Sprintf("%s: %s:%d is taken by inbound %s", site.Domain, bind, site.Port, tag))
			continue
		}
		spec.Sites = append(spec.Sites, site)
	}
	for l, tag := range xrayListens {
		if spec.HTTPPort != 0 && l.port == strconv.Itoa(spec.HTTPPort) && len(spec.Sites) > 0 {
			problems = append(problems, fmt.Sprintf("inbound %s listens on port %s which Caddy needs for certificates", tag, l.port))
		}
	}
	return spec, problems, nil
}

// nodeSetting reads key.CODE, falling back to key, then def.
func (s *Service) nodeSetting(ctx context.Context, key, code, def string) (string, error) {
	v, err := s.Setting(ctx, key+"."+code, "")
	if err != nil || v != "" {
		return v, err
	}
	return s.Setting(ctx, key, def)
}

// subscriptionSite is the subscription domain served by Caddy on the panel's server.
func (s *Service) subscriptionSite(ctx context.Context) (caddyconf.Site, bool, error) {
	domain, err := s.Setting(ctx, SettingSubDomain, "")
	if err != nil || domain == "" {
		return caddyconf.Site{}, false, err
	}
	prefix, _ := s.Setting(ctx, SettingSubPrefix, DefaultSubPrefix)
	listen, _ := s.Setting(ctx, SettingSubListen, DefaultSubListen)
	bind, _ := s.Setting(ctx, SettingSubAddress, "")
	portStr, _ := s.Setting(ctx, SettingSubPort, "443")
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return caddyconf.Site{}, false, invalid("sub.port %q is not a number", portStr)
	}
	d, _ := s.Setting(ctx, SettingSubDecoy, "docs")
	return caddyconf.Site{Domain: domain, Bind: bind, Port: port, Routes: []caddyconf.Route{
		{Kind: caddyconf.KindProxy, Upstream: listen, PathPrefix: prefix},
		{Kind: caddyconf.KindDecoy, Decoy: decoyName(d)},
	}}, true, nil
}

// PanelCaddyConfig is the Caddy config for a panel server without a local node.
func (s *Service) PanelCaddyConfig(ctx context.Context) ([]byte, error) {
	spec, _, err := s.caddySpec(ctx, &store.Node{Code: "PANEL", Local: true}, nil)
	if err != nil {
		return nil, err
	}
	return caddyconf.Build(spec)
}

func decoyName(n string) string {
	if decoy.Valid(n) {
		return n
	}
	return decoy.Names()[0]
}
