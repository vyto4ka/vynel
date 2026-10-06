// Package caddyconf builds the Caddy JSON config of a node (docs/ARCHITECTURE.md §4.4,
// docs/INBOUNDS.md §2.3–2.4). It is pure: the panel computes it, the agent only loads it.
package caddyconf

import (
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
)

// Route kinds.
const (
	KindDecoy  = "decoy"  // static decoy site (self-steal target, subscription domain root)
	KindProxy  = "proxy"  // plain reverse proxy (subscriptions)
	KindStream = "stream" // streaming reverse proxy for XHTTP: no buffering, long timeouts
)

// Route is one handler of a site, tried in order.
type Route struct {
	Kind       string
	PathPrefix string // "" matches everything
	Decoy      string // KindDecoy: site name (see package decoy)
	Upstream   string // KindProxy/KindStream: host:port
}

// Site is a domain served by Caddy.
type Site struct {
	Domain    string
	Bind      string // IP to listen on; "" = all addresses
	Port      int    // public HTTPS port when not behind Reality (default 443)
	LocalPort int    // >0: served on 127.0.0.1:LocalPort behind Reality instead
	Routes    []Route
}

// Spec is the whole node config.
type Spec struct {
	AdminAddr string // 127.0.0.1:2019
	Email     string // ACME account email (optional)
	Issuer    string // "acme" (default) or "internal" (tests)
	Sites     []Site
	HTTPBinds []string // addresses for the :80 ACME/redirect server; empty = all
	HTTPPort  int      // default 80 (tests use a high port)
}

// DecoyDirPlaceholder is expanded by Caddy from the environment the agent sets.
const DecoyDirPlaceholder = "{env.VYNEL_DECOY_DIR}"

// Build renders the Caddy JSON. Sites with the same domain and listener are merged (their
// routes concatenated in order), e.g. subscriptions and the self-steal decoy on one domain.
func Build(spec Spec) ([]byte, error) {
	if len(spec.Sites) == 0 {
		return nil, nil
	}
	type server struct {
		listen  string
		domains []string
		routes  map[string][]Route
		tls     bool
	}
	servers := map[string]*server{}
	var domains []string
	seenDomain := map[string]string{}
	for _, s := range spec.Sites {
		s.Domain = strings.ToLower(s.Domain)
		if s.Domain == "" {
			return nil, fmt.Errorf("site without domain")
		}
		listen := listenAddr(s)
		if prev, ok := seenDomain[s.Domain]; ok && prev != listen {
			return nil, fmt.Errorf("domain %s is served on both %s and %s", s.Domain, prev, listen)
		}
		seenDomain[s.Domain] = listen
		srv := servers[listen]
		if srv == nil {
			srv = &server{listen: listen, routes: map[string][]Route{}, tls: true}
			servers[listen] = srv
		}
		if _, ok := srv.routes[s.Domain]; !ok {
			srv.domains = append(srv.domains, s.Domain)
			domains = append(domains, s.Domain)
		}
		srv.routes[s.Domain] = append(srv.routes[s.Domain], s.Routes...)
	}

	httpServers := map[string]any{}
	names := make([]string, 0, len(servers))
	for l := range servers {
		names = append(names, l)
	}
	sort.Strings(names)
	for i, l := range names {
		srv := servers[l]
		var routes, errRoutes []any
		routes = append(routes, map[string]any{"handle": []any{hideServer()}})
		for _, d := range srv.domains {
			sub, decoy, err := siteRoutes(srv.routes[d])
			if err != nil {
				return nil, fmt.Errorf("%s: %w", d, err)
			}
			routes = append(routes, map[string]any{
				"match":    []any{map[string]any{"host": []string{d}}},
				"handle":   []any{map[string]any{"handler": "subroute", "routes": sub}},
				"terminal": true,
			})
			if decoy != "" {
				errRoutes = append(errRoutes, map[string]any{
					"match": []any{map[string]any{"host": []string{d}}},
					"handle": []any{hideServer(),
						map[string]any{"handler": "rewrite", "uri": "/404.html"},
						map[string]any{"handler": "file_server", "root": DecoyDirPlaceholder + "/" + decoy, "status_code": "{http.error.status_code}"}},
				})
			}
		}
		s := map[string]any{
			"listen":                  []string{l},
			"routes":                  routes,
			"tls_connection_policies": []any{map[string]any{}},
			"automatic_https":         map[string]any{"disable_redirects": true},
			"max_header_bytes":        256 << 10, // XHTTP uplink data travels in cookies
		}
		if len(errRoutes) > 0 {
			s["errors"] = map[string]any{"routes": errRoutes}
		}
		httpServers["https"+strconv.Itoa(i)] = s
	}

	// :80 answers ACME HTTP-01 challenges (every Caddy server does) and redirects to HTTPS.
	httpPort := spec.HTTPPort
	if httpPort == 0 {
		httpPort = 80
	}
	binds := []string{":" + strconv.Itoa(httpPort)}
	if len(spec.HTTPBinds) > 0 {
		binds = binds[:0]
		for _, b := range spec.HTTPBinds {
			binds = append(binds, net.JoinHostPort(b, strconv.Itoa(httpPort)))
		}
	}
	httpServers["http"] = map[string]any{
		"listen": binds,
		"routes": []any{map[string]any{"handle": []any{hideServer(), map[string]any{
			"handler": "static_response", "status_code": 301,
			"headers": map[string]any{"Location": []string{"https://{http.request.host}{http.request.uri}"}},
		}}}},
		"automatic_https": map[string]any{"disable": true},
	}

	issuer := map[string]any{"module": "acme", "challenges": map[string]any{"tls-alpn": map[string]any{"disabled": true}}}
	if spec.Email != "" {
		issuer["email"] = spec.Email
	}
	if spec.Issuer == "internal" {
		issuer = map[string]any{"module": "internal"}
	}
	sort.Strings(domains)
	admin := spec.AdminAddr
	if admin == "" {
		admin = "127.0.0.1:2019"
	}
	cfg := map[string]any{
		"admin": map[string]any{"listen": admin},
		"apps": map[string]any{
			"http": map[string]any{"http_port": httpPort, "https_port": 443, "servers": httpServers},
			"tls": map[string]any{
				"certificates": map[string]any{"automate": domains},
				"automation":   map[string]any{"policies": []any{map[string]any{"subjects": domains, "issuers": []any{issuer}}}},
			},
		},
	}
	return json.Marshal(cfg)
}

func listenAddr(s Site) string {
	if s.LocalPort > 0 {
		return "127.0.0.1:" + strconv.Itoa(s.LocalPort)
	}
	port := s.Port
	if port == 0 {
		port = 443
	}
	return net.JoinHostPort(s.Bind, strconv.Itoa(port))
}

func hideServer() map[string]any {
	return map[string]any{"handler": "headers", "response": map[string]any{"deferred": true, "delete": []string{"Server", "Via"}}}
}

// siteRoutes renders the routes of one domain and returns the decoy used for its 404 page.
// Routes with a path prefix go first, so a catch-all decoy never shadows them.
func siteRoutes(rs []Route) ([]any, string, error) {
	rs = append([]Route(nil), rs...)
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].PathPrefix != "" && rs[j].PathPrefix == "" })
	var out []any
	decoy := ""
	for _, r := range rs {
		var handler map[string]any
		switch r.Kind {
		case KindDecoy:
			if r.Decoy == "" {
				return nil, "", fmt.Errorf("decoy route without a site")
			}
			decoy = r.Decoy
			handler = map[string]any{"handler": "file_server", "root": DecoyDirPlaceholder + "/" + r.Decoy}
		case KindProxy:
			handler = map[string]any{"handler": "reverse_proxy", "upstreams": []any{map[string]any{"dial": r.Upstream}}}
		case KindStream:
			handler = map[string]any{
				"handler": "reverse_proxy", "upstreams": []any{map[string]any{"dial": r.Upstream}},
				"flush_interval": -1,
				"transport":      map[string]any{"protocol": "http", "versions": []string{"1.1"}, "read_timeout": "1h", "write_timeout": "1h"},
			}
		default:
			return nil, "", fmt.Errorf("unknown route kind %q", r.Kind)
		}
		route := map[string]any{"handle": []any{handler}, "terminal": true}
		if r.PathPrefix != "" {
			route["match"] = []any{map[string]any{"path": []string{r.PathPrefix + "*"}}}
		}
		out = append(out, route)
	}
	return out, decoy, nil
}
