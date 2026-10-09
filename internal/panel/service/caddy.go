package service

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/vyto4ka/vynel/internal/caddyconf"
	"github.com/vyto4ka/vynel/internal/decoy"
	"github.com/vyto4ka/vynel/internal/panel/store"
	"github.com/vyto4ka/vynel/internal/xrayconf"
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
		if xrayconf.Transport(r.Inbound) == "udp" {
			continue // Hysteria on UDP 443 does not compete with Caddy or Reality on TCP 443
		}
		xrayListens[listener{listen, strconv.Itoa(port)}] = r.Tag
		ss, _ := r.Inbound["streamSettings"].(map[string]any)
		if ss["security"] == "reality" {
			rs, _ := ss["realitySettings"].(map[string]any)
			f := realityFront{tag: r.Tag, listen: listen, port: port}
			if host, p, err := net.SplitHostPort(str(rs["target"])); err == nil && (host == "127.0.0.1" || host == "localhost") {
				f.localPort, _ = strconv.Atoi(p)
				// Reality hands the client's address to our Caddy (PROXY v2), so subscriptions, the
				// panel and the firewall see who connects instead of 127.0.0.1.
				rs["xver"] = 2
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
			sites = append(sites, caddyconf.Site{Domain: str(cm["domain"]), LocalPort: front.localPort, ProxyProtocol: true,
				Routes: []caddyconf.Route{{Kind: caddyconf.KindDecoy, Decoy: decoyName(str(cm["decoy"]))}}})
		case "certificate":
			// The inbound needs Caddy's certificate for the domain (Hysteria2 uses it directly).
			// The site lives on 127.0.0.1 only: the certificate comes over HTTP-01 on :80, and when a
			// Reality inbound on the same node targets this port, browsers see the same decoy there.
			port, _ := strconv.Atoi(str(cm["local_port"]))
			if port == 0 {
				port = 8443
			}
			sites = append(sites, caddyconf.Site{Domain: str(cm["domain"]), LocalPort: port,
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
		panelSites, err := s.panelSites(ctx)
		if err != nil {
			return caddyconf.Spec{}, nil, err
		}
		sites = append(sites, panelSites...)
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
			site.ProxyProtocol = true
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

// panelSites are the sites of the panel's own server: the subscription domain with the web
// panel under its secret path, or the web panel on its own domain (web.domain), port
// (web.port) or IP (web.address), each with its own decoy (docs/STEALTH.md §1).
func (s *Service) panelSites(ctx context.Context) ([]caddyconf.Site, error) {
	domain, err := s.Setting(ctx, SettingSubDomain, "")
	if err != nil {
		return nil, err
	}
	webDomain, _ := s.Setting(ctx, SettingWebDomain, "")
	webDomain = strings.ToLower(strings.TrimSpace(webDomain))
	if domain == "" && webDomain == "" {
		return nil, nil
	}
	bind, _ := s.Setting(ctx, SettingSubAddress, "")
	portStr, _ := s.Setting(ctx, SettingSubPort, "443")
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, invalid("sub.port %q is not a number", portStr)
	}
	webPort, err := strconv.Atoi(s.webPort(ctx))
	if err != nil || webPort <= 0 || webPort > 65535 {
		return nil, invalid("web.port %q is not a port", s.webPort(ctx))
	}
	webBind, _ := s.Setting(ctx, SettingWebAddress, "")
	if webBind == "" {
		webBind = bind
	}
	d, _ := s.Setting(ctx, SettingSubDecoy, "docs")
	decoyRoute := caddyconf.Route{Kind: caddyconf.KindDecoy, Decoy: decoyName(d)}
	wd, _ := s.Setting(ctx, SettingWebDecoy, "")
	webDecoyRoute := decoyRoute
	if wd != "" {
		webDecoyRoute.Decoy = decoyName(wd)
	}
	var webRoute *caddyconf.Route
	if path, _ := s.WebPath(ctx); path != "" {
		listen, _ := s.Setting(ctx, SettingWebListen, DefaultWebListen)
		webRoute = &caddyconf.Route{Kind: caddyconf.KindProxy, Upstream: listen, PathPrefix: path}
	}
	if webDomain == "" {
		webDomain = domain
	}
	// The panel shares the subscription site only when domain, IP and port are all the same.
	shared := webDomain == domain && webPort == port && webBind == bind
	var sites []caddyconf.Site
	if domain != "" {
		prefix := s.SubPrefix(ctx)
		listen, _ := s.Setting(ctx, SettingSubListen, DefaultSubListen)
		site := caddyconf.Site{Domain: domain, Bind: bind, Port: port, Routes: []caddyconf.Route{{Kind: caddyconf.KindProxy, Upstream: listen, PathPrefix: prefix}}}
		if webRoute != nil && shared {
			site.Routes = append(site.Routes, *webRoute)
		}
		site.Routes = append(site.Routes, decoyRoute)
		sites = append(sites, site)
	}
	if webRoute != nil && !shared {
		sites = append(sites, caddyconf.Site{Domain: webDomain, Bind: webBind, Port: webPort, Routes: []caddyconf.Route{*webRoute, webDecoyRoute}})
	}
	return sites, nil
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
