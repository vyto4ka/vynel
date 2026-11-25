package service

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"

	"github.com/vyto4ka/vynel/internal/caddyconf"
	"github.com/vyto4ka/vynel/internal/nginxconf"
	"github.com/vyto4ka/vynel/internal/panel/store"
	"github.com/vyto4ka/vynel/internal/xrayconf"
)

// CapNginx is the agent capability to run the nginx config (Snapshot.nginx_config).
const CapNginx = "nginx"

// nginxFront is a Reality inbound with its listener, target and names.
type nginxFront struct {
	realityFront
	inbound map[string]any
	names   []string
	routed  bool // public sites share its (IP, port): nginx routes by SNI in front of it
}

// covers reports whether every name a site answers on is one of the Reality inbound's names.
func (f *nginxFront) covers(site nginxconf.Site) bool {
	if site.Default {
		return false // a default site must also get the names nobody else has
	}
	for _, n := range append([]string{site.Domain}, site.Names...) {
		if !slices.ContainsFunc(f.names, func(r string) bool { return strings.EqualFold(r, n) }) {
			return false
		}
	}
	return true
}

// nginxSpec builds the node's nginx config from its inbounds and, for the panel's own server,
// the panel's sites (docs/INBOUNDS.md §2.3). Sites on loopback are Reality targets. A public
// site whose (IP, port) a Reality inbound holds does not hide behind Reality as it did with
// Caddy: nginx takes the port and routes by SNI — Reality's names go to Xray, now on loopback,
// anything else (other names, no SNI) to nginx's HTTPS listener, which is Reality's target too.
// The rendered Reality inbounds are changed in place (listen, port, PROXY), so call this
// before building the Xray config.
func (s *Service) nginxSpec(ctx context.Context, n *store.Node, inbounds []*xrayconf.RenderedInbound) (nginxconf.Spec, []string, error) {
	var problems []string
	enabled, err := s.nodeSetting(ctx, SettingCaddyEnabled, n.Code, "true")
	if err != nil || enabled == "false" {
		return nginxconf.Spec{}, nil, err
	}
	spec := nginxconf.Spec{}
	spec.Email, _ = s.Setting(ctx, SettingCaddyEmail, "")
	spec.Issuer, _ = s.Setting(ctx, SettingCaddyIssuer, "acme")
	if hp, _ := s.Setting(ctx, SettingCaddyHTTPPort, "80"); hp != "" {
		spec.HTTPPort, _ = strconv.Atoi(hp)
	}

	type listener struct{ ip, port string }
	var fronts []*nginxFront
	xrayListens := map[listener]string{}
	for _, r := range inbounds {
		listen, _ := r.Inbound["listen"].(string)
		if listen == "" {
			listen = "0.0.0.0"
		}
		port, _ := r.Inbound["port"].(int)
		if xrayconf.Transport(r.Inbound) == "udp" {
			continue // Hysteria on UDP 443 does not compete with nginx or Reality on TCP 443
		}
		xrayListens[listener{listen, strconv.Itoa(port)}] = r.Tag
		ss, _ := r.Inbound["streamSettings"].(map[string]any)
		if ss["security"] != "reality" {
			continue
		}
		rs, _ := ss["realitySettings"].(map[string]any)
		f := &nginxFront{realityFront: realityFront{tag: r.Tag, listen: listen, port: port}, inbound: r.Inbound}
		for _, sn := range anyList(rs["serverNames"]) {
			if v, ok := sn.(string); ok && v != "" {
				f.names = append(f.names, v)
			}
		}
		if host, p, err := net.SplitHostPort(str(rs["target"])); err == nil && (host == "127.0.0.1" || host == "localhost") {
			f.localPort, _ = strconv.Atoi(p)
			// Reality hands the client's address to nginx (PROXY v2).
			rs["xver"] = 2
		}
		fronts = append(fronts, f)
	}
	frontOf := func(tag string) *nginxFront {
		for _, f := range fronts {
			if f.tag == tag {
				return f
			}
		}
		return nil
	}

	var sites []nginxconf.Site
	for _, r := range inbounds {
		if r.Template == nil || r.Template.Web() == nil {
			continue
		}
		vals := copyMap(r.Values)
		vals["INBOUND_PORT"], _ = r.Inbound["port"].(int)
		cm, err := xrayconf.ExpandTree(r.Template.Web(), vals)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: nginx: %v", r.Tag, err))
			continue
		}
		var names []string
		for _, x := range anyList(cm["server_names"]) {
			if v := str(x); v != "" {
				names = append(names, v)
			}
		}
		dn, dp := nodeDecoy(r.Tag, cm, n.DecoyPath, &problems)
		decoyRoute := nginxconf.Route{Kind: nginxconf.KindDecoy, Decoy: dn, DecoyPath: dp}
		switch str(cm["role"]) {
		case "selfsteal":
			front := frontOf(r.Tag)
			if front == nil || front.localPort == 0 {
				continue // e.g. a profile override points Reality at a foreign site
			}
			sites = append(sites, nginxconf.Site{Domain: str(cm["domain"]), Names: names, LocalPort: front.localPort, ProxyProtocol: true,
				Server: str(cm["server"]), Routes: []nginxconf.Route{decoyRoute}})
		case "certificate":
			// The inbound needs the certificate of the domain (Hysteria2 reads the files). The site
			// lives on 127.0.0.1 only; a Reality inbound targeting the same port shares it.
			port, _ := strconv.Atoi(str(cm["local_port"]))
			if port == 0 {
				port = 8443
			}
			sites = append(sites, nginxconf.Site{Domain: str(cm["domain"]), Names: names, LocalPort: port,
				Server: str(cm["server"]), Routes: []nginxconf.Route{decoyRoute}})
		case "reverse_proxy_xhttp":
			bind := str(vals["LISTEN_IP"])
			if bind == "0.0.0.0" {
				bind = ""
			}
			route := nginxconf.Route{Kind: nginxconf.KindStream, Upstream: str(cm["upstream"]), PathPrefix: str(cm["path"])}
			if loc := str(cm["location"]); loc != "" {
				route = nginxconf.Route{Kind: nginxconf.KindRaw, Raw: loc, PathPrefix: str(cm["path"])}
			}
			// A CDN origin answers any name and connections without SNI: CDNs may come with
			// their own domain in SNI and Host, or with none.
			sites = append(sites, nginxconf.Site{Domain: str(cm["domain"]), Names: names, Bind: bind, Port: 443, Default: true,
				Server: str(cm["server"]), Routes: []nginxconf.Route{route, decoyRoute}})
		}
	}
	if n.Local {
		ps, siteProblems, err := s.panelSites(ctx)
		if err != nil {
			return nginxconf.Spec{}, nil, err
		}
		problems = append(problems, siteProblems...)
		sites = withPanelSites(sites, ps, nginxSites)
	}

	localPorts := map[int]bool{}
	for _, site := range sites {
		if site.LocalPort > 0 {
			localPorts[site.LocalPort] = true
		}
	}
	for _, site := range sites {
		if site.LocalPort > 0 {
			if tag, ok := xrayListens[listener{"127.0.0.1", strconv.Itoa(site.LocalPort)}]; ok {
				problems = append(problems, fmt.Sprintf("%s: nginx needs 127.0.0.1:%d but inbound %s listens there", site.Domain, site.LocalPort, tag))
				continue
			}
			if tag, ok := xrayListens[listener{"0.0.0.0", strconv.Itoa(site.LocalPort)}]; ok {
				problems = append(problems, fmt.Sprintf("%s: nginx needs 127.0.0.1:%d but inbound %s listens on all addresses there", site.Domain, site.LocalPort, tag))
				continue
			}
			spec.Sites = append(spec.Sites, site)
			continue
		}
		port := site.Port
		if port == 0 {
			port = 443
		}
		var front *nginxFront
		for _, f := range fronts {
			if f.port == port && (f.listen == "0.0.0.0" || site.Bind == "" || f.listen == site.Bind) {
				front = f
				break
			}
		}
		if front != nil && front.localPort != 0 && front.covers(site) {
			// Every name of the site is a Reality name: Xray hands such connections to its
			// target anyway, so the site joins the target and Reality keeps the port to itself.
			site.Bind, site.Port, site.LocalPort, site.ProxyProtocol = "", 0, front.localPort, true
			spec.Sites = append(spec.Sites, site)
			continue
		}
		if front != nil {
			if front.localPort == 0 {
				problems = append(problems, fmt.Sprintf("%s: port %d is taken by Reality inbound %s whose target is not local, the site cannot share it", site.Domain, port, front.tag))
				continue
			}
			front.routed = true
			site.Bind, site.Port, site.LocalPort, site.ProxyProtocol = "", 0, front.localPort, true
			spec.Sites = append(spec.Sites, site)
			continue
		}
		bind := site.Bind
		if bind == "" {
			bind = "0.0.0.0"
		}
		if tag, ok := xrayListens[listener{bind, strconv.Itoa(port)}]; ok {
			problems = append(problems, fmt.Sprintf("%s: %s:%d is taken by inbound %s", site.Domain, bind, port, tag))
			continue
		}
		spec.Sites = append(spec.Sites, site)
	}

	// Reality inbounds sharing one (IP, port) cannot both bind it: nginx takes the port and the
	// SNI router splits them by their server names (Reality raw and Reality XHTTP on one 443).
	// Every front of a shared port moves; the fallback chain still ends at a decoy, because a
	// front hands a non-Reality connection to its own target, the HTTPS listener behind nginx.
	byListener := map[string][]*nginxFront{}
	for _, f := range fronts {
		k := f.listen + "\x00" + strconv.Itoa(f.port)
		byListener[k] = append(byListener[k], f)
	}
	for _, group := range byListener {
		if len(group) < 2 {
			continue
		}
		sniOwner := map[string]string{}
		for _, f := range group {
			if f.localPort == 0 {
				problems = append(problems, fmt.Sprintf("%s shares port %d with Reality inbound %s but its target is not local, the SNI router needs a local target", f.tag, f.port, group[0].tag))
				continue
			}
			f.routed = true
			for _, name := range f.names {
				n := strings.ToLower(strings.TrimSpace(name))
				if n == "" {
					continue
				}
				if prev, dup := sniOwner[n]; dup && prev != f.tag {
					problems = append(problems, fmt.Sprintf("%s shares port %d and SNI %s with %s: use different domains or ports so the SNI router can tell them apart", f.tag, f.port, n, prev))
				} else {
					sniOwner[n] = f.tag
				}
			}
		}
	}

	// SNI routers: the Reality inbounds move to loopback ports and read the PROXY header nginx
	// sends; their public (IP, port) becomes nginx's. Fronts of one (IP, port) share a single
	// router, each with its own loopback backend.
	routers := map[string]*nginxconf.Stream{}
	var routerKeys []string
	for _, f := range fronts {
		if !f.routed {
			continue
		}
		internal := 10000 + f.port
		for {
			p := strconv.Itoa(internal)
			_, a := xrayListens[listener{"127.0.0.1", p}]
			_, b := xrayListens[listener{"0.0.0.0", p}]
			if !a && !b && !localPorts[internal] && internal != spec.HTTPPort {
				break
			}
			internal++
		}
		xrayListens[listener{"127.0.0.1", strconv.Itoa(internal)}] = f.tag
		bind := f.listen
		if bind == "0.0.0.0" {
			bind = ""
		}
		key := bind + "\x00" + strconv.Itoa(f.port)
		st := routers[key]
		if st == nil {
			st = &nginxconf.Stream{Bind: bind, Port: f.port, Default: "127.0.0.1:" + strconv.Itoa(f.localPort)}
			routers[key] = st
			routerKeys = append(routerKeys, key)
		}
		st.Backends = append(st.Backends, nginxconf.Backend{Names: f.names, Addr: "127.0.0.1:" + strconv.Itoa(internal)})
		f.inbound["listen"] = "127.0.0.1"
		f.inbound["port"] = internal
		ss, _ := f.inbound["streamSettings"].(map[string]any)
		sock, _ := ss["sockopt"].(map[string]any)
		if sock == nil {
			sock = map[string]any{}
			ss["sockopt"] = sock
		}
		sock["acceptProxyProtocol"] = true
	}
	for _, key := range routerKeys {
		spec.Streams = append(spec.Streams, *routers[key])
	}
	for l, tag := range xrayListens {
		if spec.HTTPPort != 0 && l.port == strconv.Itoa(spec.HTTPPort) && (len(spec.Sites) > 0 || len(spec.Streams) > 0) {
			problems = append(problems, fmt.Sprintf("inbound %s listens on port %s which nginx needs for certificates", tag, l.port))
		}
	}
	return spec, problems, nil
}

// nginxSites converts the panel's sites (shared with the Caddy layout) to nginx sites.
func nginxSites(in []caddyconf.Site) []nginxconf.Site {
	out := make([]nginxconf.Site, 0, len(in))
	for _, s := range in {
		ns := nginxconf.Site{Domain: s.Domain, Bind: s.Bind, Port: s.Port, LocalPort: s.LocalPort, ProxyProtocol: s.ProxyProtocol}
		for _, r := range s.Routes {
			ns.Routes = append(ns.Routes, nginxconf.Route{Kind: r.Kind, PathPrefix: r.PathPrefix, Decoy: r.Decoy, DecoyPath: r.DecoyPath, Upstream: r.Upstream, InterceptErrors: r.InterceptErrors})
		}
		out = append(out, ns)
	}
	return out
}

// PanelNginxConfig is the nginx config for a panel server without a local node.
func (s *Service) PanelNginxConfig(ctx context.Context) (string, error) {
	spec, _, err := s.nginxSpec(ctx, &store.Node{Code: "PANEL", Local: true}, nil)
	if err != nil {
		return "", err
	}
	return nginxconf.Build(spec)
}

func anyList(v any) []any {
	l, _ := v.([]any)
	return l
}
