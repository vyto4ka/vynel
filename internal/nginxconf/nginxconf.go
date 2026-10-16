// Package nginxconf builds the nginx config of a node or of the panel's own server
// (docs/ARCHITECTURE.md §4.4, docs/INBOUNDS.md §2.3). It is pure: the panel computes the
// config, the agent fills in its local paths (see the @@...@@ tokens) and runs nginx.
//
// Layout:
//   - stream {}: on a public port shared by several TLS consumers, ssl_preread routes each
//     connection by SNI (Reality inbounds of Xray, the HTTPS sites of this config); anything
//     else, including connections without SNI, goes to the HTTPS sites;
//   - http {}: HTTPS sites grouped by listener. A connection with a name no site has, or with
//     no name at all, is cut at the handshake (strict SNI: a scan by IP learns no domain) —
//     unless a site on that listener is marked Default (a CDN origin: CDNs may come with their
//     own name or none), then that site answers it, like a lone nginx with one site does;
//   - :80 answers ACME HTTP-01 and redirects to HTTPS.
package nginxconf

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Route kinds.
const (
	KindDecoy  = "decoy"  // static decoy site (self-steal target, subscription domain root)
	KindProxy  = "proxy"  // plain reverse proxy (subscriptions, the web panel)
	KindStream = "stream" // streaming reverse proxy for XHTTP: no buffering, long timeouts
	KindRaw    = "raw"    // directives written in a profile template, placed in the location as is
)

// Tokens the agent replaces with its own paths and capabilities before running nginx.
const (
	TokModules = "@@MODULES@@" // load_module lines (stream), or nothing when built in
	TokUser    = "@@USER@@"    // "user www-data;" or nothing
	TokRun     = "@@RUN@@"     // runtime dir: pid, temp files, error log
	TokCerts   = "@@CERTS@@"   // certificate store: <domain>/fullchain.pem, <domain>/privkey.pem
	TokDecoy   = "@@DECOY@@"   // decoy sites: <name>/index.html ...
	TokACME    = "@@ACME@@"    // ACME HTTP-01 webroot
	TokIPv6    = "#ipv6#"      // line prefix: uncommented when the host has IPv6
	TokReject  = "@@REJECT@@"  // cuts a TLS handshake: ssl_reject_handshake, or a dummy certificate on old nginx
)

// Route is one location of a site.
type Route struct {
	Kind       string
	PathPrefix string // "" matches everything
	Decoy      string // KindDecoy: site name (see package decoy)
	Upstream   string // KindProxy/KindStream: host:port
	Raw        string // KindRaw: directives inside the location block
	// InterceptErrors (KindProxy): the upstream's 404 becomes the site's own 404 page, so a
	// path served by the panel looks like any other missing page of the site.
	InterceptErrors bool
}

// Site is a domain served over HTTPS.
type Site struct {
	Domain    string   // certificate name and first server_name
	Names     []string // more server_name values without a certificate of their own (a CDN domain)
	Bind      string   // IP to listen on; "" = all addresses
	Port      int      // public HTTPS port (default 443)
	LocalPort int      // >0: served on 127.0.0.1:LocalPort (behind Reality or the SNI router)
	Routes    []Route
	Server    string // directives at server level, written in a profile template
	// Default: the site also answers names no other site on its listener has and connections
	// without SNI (a CDN origin). Without one, such connections are cut at the handshake.
	Default bool
	// ProxyProtocol: the listener expects a PROXY header (from Reality's xver or the SNI
	// router), so the backends see the client's address instead of 127.0.0.1.
	ProxyProtocol bool
}

// Backend is where the SNI router sends connections for some server names.
type Backend struct {
	Names []string
	Addr  string // host:port
}

// Stream is an SNI router on a public listener. Every backend gets a PROXY header.
type Stream struct {
	Bind     string // "" = all addresses
	Port     int
	Backends []Backend
	Default  string // host:port for any other name and for connections without SNI
}

// Spec is the whole config.
type Spec struct {
	Email     string // ACME account email (optional)
	Issuer    string // "acme" (default) or "internal" (self-signed CA, tests)
	Sites     []Site
	Streams   []Stream
	HTTPBinds []string // addresses for the :80 ACME/redirect server; empty = all
	HTTPPort  int      // default 80 (tests use a high port)
}

var (
	nameRe   = regexp.MustCompile(`^[a-z0-9*]([a-z0-9.*-]*[a-z0-9])?$`)
	prefixRe = regexp.MustCompile(`^/[A-Za-z0-9._~!$&'()*+,;=:@%/-]*$`)
)

// Build renders nginx.conf. Sites with the same domain and listener are merged (their routes
// concatenated in order), e.g. subscriptions and the self-steal decoy on one domain.
func Build(spec Spec) (string, error) {
	if len(spec.Sites) == 0 && len(spec.Streams) == 0 {
		return "", nil
	}
	type server struct {
		listen   string
		bind     string
		port     int
		local    bool
		proxy    bool
		domains  []string
		def      string // the Default site, if any
		names    map[string][]string
		routes   map[string][]Route
		snippets map[string][]string
	}
	servers := map[string]*server{}
	for _, s := range spec.Sites {
		s.Domain = strings.ToLower(strings.TrimSpace(s.Domain))
		if !nameRe.MatchString(s.Domain) || strings.Contains(s.Domain, "*") {
			return "", fmt.Errorf("site domain %q is not a domain", s.Domain)
		}
		key, bind, port, local := listener(s)
		srv := servers[key]
		if srv == nil {
			srv = &server{listen: key, bind: bind, port: port, local: local,
				names: map[string][]string{}, routes: map[string][]Route{}, snippets: map[string][]string{}}
			servers[key] = srv
		}
		if _, ok := srv.routes[s.Domain]; !ok {
			srv.domains = append(srv.domains, s.Domain)
			srv.routes[s.Domain] = nil
		}
		for _, n := range s.Names {
			n = strings.ToLower(strings.TrimSpace(n))
			if n == "" || n == s.Domain {
				continue
			}
			if !nameRe.MatchString(n) {
				return "", fmt.Errorf("%s: server name %q is not a domain", s.Domain, n)
			}
			srv.names[s.Domain] = appendUnique(srv.names[s.Domain], n)
		}
		srv.routes[s.Domain] = append(srv.routes[s.Domain], s.Routes...)
		if strings.TrimSpace(s.Server) != "" {
			if err := checkSnippet(s.Server); err != nil {
				return "", fmt.Errorf("%s: server directives: %w", s.Domain, err)
			}
			srv.snippets[s.Domain] = append(srv.snippets[s.Domain], strings.TrimSpace(s.Server))
		}
		srv.proxy = srv.proxy || s.ProxyProtocol
		if s.Default && srv.def == "" {
			srv.def = s.Domain
		}
	}
	// A name may belong to one site per listener only: nginx would pick one silently.
	for _, srv := range servers {
		owner := map[string]string{}
		for _, d := range srv.domains {
			for _, n := range append([]string{d}, srv.names[d]...) {
				if o, ok := owner[n]; ok && o != d {
					return "", fmt.Errorf("server name %s is used by both %s and %s on %s", n, o, d, srv.listen)
				}
				owner[n] = d
			}
		}
	}

	var b strings.Builder
	w := func(indent int, format string, args ...any) {
		b.WriteString(strings.Repeat("    ", indent))
		fmt.Fprintf(&b, format, args...)
		b.WriteByte('\n')
	}
	w(0, "# vynel: generated by the panel, changes are overwritten")
	if spec.Email != "" {
		w(0, "# vynel-acme-email: %s", oneLine(spec.Email))
	}
	issuer := spec.Issuer
	if issuer == "" {
		issuer = "acme"
	}
	w(0, "# vynel-acme-issuer: %s", oneLine(issuer))
	w(0, "%s", TokModules)
	w(0, "%s", TokUser)
	w(0, "worker_processes auto;")
	w(0, "worker_rlimit_nofile 65535;")
	w(0, "pid %s/nginx.pid;", TokRun)
	w(0, "error_log %s/error.log warn;", TokRun)
	w(0, "events { worker_connections 8192; }")

	if len(spec.Streams) > 0 {
		w(0, "")
		w(0, "stream {")
		w(1, "proxy_connect_timeout 5s;")
		w(1, "proxy_timeout 1h;")
		w(1, "preread_timeout 10s;")
		for i, st := range spec.Streams {
			if st.Default == "" {
				return "", fmt.Errorf("stream on port %d without a default backend", st.Port)
			}
			v := "$vynel_sni_" + strconv.Itoa(i)
			w(1, "map $ssl_preread_server_name %s {", v)
			w(2, "hostnames;")
			seen := map[string]bool{}
			for _, be := range st.Backends {
				if err := checkAddr(be.Addr); err != nil {
					return "", err
				}
				for _, n := range be.Names {
					n = strings.ToLower(strings.TrimSpace(n))
					if n == "" || seen[n] {
						continue
					}
					if !nameRe.MatchString(n) {
						return "", fmt.Errorf("stream: server name %q is not a domain", n)
					}
					seen[n] = true
					w(2, "%s %s;", n, be.Addr)
				}
			}
			if err := checkAddr(st.Default); err != nil {
				return "", err
			}
			w(2, "default %s;", st.Default)
			w(1, "}")
			w(1, "server {")
			for _, l := range listenLines(st.Bind, st.Port, "reuseport") {
				w(2, "%s", l)
			}
			w(2, "ssl_preread on;")
			w(2, "proxy_protocol on;")
			w(2, "proxy_pass %s;", v)
			w(1, "}")
		}
		w(0, "}")
	}

	w(0, "")
	w(0, "http {")
	w(1, "server_tokens off;")
	w(1, "access_log off;")
	w(1, "sendfile on;")
	w(1, "tcp_nodelay on;")
	w(1, "keepalive_timeout 75s;")
	w(1, "types_hash_max_size 2048;")
	w(1, "default_type application/octet-stream;")
	w(1, "types {")
	for _, t := range mimeTypes {
		w(2, "%s", t)
	}
	w(1, "}")
	w(1, "charset utf-8;")
	w(1, "charset_types text/css application/javascript image/svg+xml;")
	w(1, "gzip on;")
	w(1, "gzip_types text/css application/javascript image/svg+xml application/json;")
	w(1, "# XHTTP may carry uplink data in cookies and long query strings")
	w(1, "client_header_buffer_size 4k;")
	w(1, "large_client_header_buffers 8 256k;")
	w(1, "client_max_body_size 1m;")
	for _, t := range []string{"client_body", "proxy", "fastcgi", "uwsgi", "scgi"} {
		w(1, "%s_temp_path %s/%s;", t, TokRun, t)
	}
	w(1, "ssl_protocols TLSv1.2 TLSv1.3;")
	w(1, "ssl_prefer_server_ciphers off;")
	w(1, "ssl_session_cache shared:vynel:10m;")
	w(1, "ssl_session_timeout 1d;")
	w(1, "ssl_session_tickets off;")
	w(1, "set_real_ip_from 127.0.0.1;")
	w(1, "set_real_ip_from ::1;")
	w(1, "proxy_http_version 1.1;")
	w(1, "proxy_set_header Connection \"\";")

	// :80: ACME HTTP-01 and the redirect to HTTPS.
	httpPort := spec.HTTPPort
	if httpPort == 0 {
		httpPort = 80
	}
	w(1, "server {")
	if len(spec.HTTPBinds) == 0 {
		for _, l := range listenLines("", httpPort, "default_server") {
			w(2, "%s", l)
		}
	} else {
		for _, bind := range spec.HTTPBinds {
			for _, l := range listenLines(bind, httpPort, "default_server") {
				w(2, "%s", l)
			}
		}
	}
	w(2, "server_name _;")
	w(2, "location ^~ /.well-known/acme-challenge/ {")
	w(3, "root %s;", TokACME)
	w(3, "default_type text/plain;")
	w(3, "try_files $uri =404;")
	w(2, "}")
	w(2, "location / { return 301 https://$host$request_uri; }")
	w(1, "}")

	keys := make([]string, 0, len(servers))
	for k := range servers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		srv := servers[k]
		opts := "ssl"
		if srv.proxy {
			opts += " proxy_protocol"
		}
		listens := listenLines(srv.bind, srv.port, opts+" http2")
		if srv.local {
			listens = []string{fmt.Sprintf("listen 127.0.0.1:%d %s http2;", srv.port, opts)}
		}
		realIP := func(indent int) {
			if srv.proxy {
				w(indent, "real_ip_header proxy_protocol;")
			}
		}
		w(1, "")
		w(1, "# %s", srv.listen)
		if srv.def == "" {
			// Strict SNI: unknown names and connections without one get no certificate.
			w(1, "server {")
			for _, l := range listens {
				w(2, "%s", strings.Replace(l, ";", " default_server;", 1))
			}
			w(2, "server_name _;")
			w(2, "%s", TokReject)
			w(2, "return 444;")
			w(1, "}")
		}

		for _, d := range srv.domains {
			w(1, "server {")
			for _, l := range listens {
				if d == srv.def {
					l = strings.Replace(l, ";", " default_server;", 1)
				}
				w(2, "%s", l)
			}
			w(2, "server_name %s;", strings.Join(append([]string{d}, srv.names[d]...), " "))
			realIP(2)
			certLines(w, 2, d)
			w(2, "add_header Strict-Transport-Security \"max-age=31536000\" always;")
			for _, sn := range srv.snippets[d] {
				for _, line := range strings.Split(sn, "\n") {
					w(2, "%s", strings.TrimRight(line, " \t"))
				}
			}
			if err := locations(w, srv.routes[d]); err != nil {
				return "", fmt.Errorf("%s: %w", d, err)
			}
			w(1, "}")
		}
	}
	w(0, "}")
	return b.String(), nil
}

func listener(s Site) (key, bind string, port int, local bool) {
	if s.LocalPort > 0 {
		return "127.0.0.1:" + strconv.Itoa(s.LocalPort), "127.0.0.1", s.LocalPort, true
	}
	port = s.Port
	if port == 0 {
		port = 443
	}
	return net.JoinHostPort(s.Bind, strconv.Itoa(port)), s.Bind, port, false
}

// listenLines returns the listen directives for bind:port; all addresses also get an IPv6
// line the agent enables only when the host has IPv6.
func listenLines(bind string, port int, opts string) []string {
	suffix := ""
	if opts != "" {
		suffix = " " + opts
	}
	if bind == "" || bind == "0.0.0.0" {
		return []string{
			fmt.Sprintf("listen %d%s;", port, suffix),
			fmt.Sprintf("%slisten [::]:%d%s;", TokIPv6, port, suffix),
		}
	}
	return []string{fmt.Sprintf("listen %s%s;", net.JoinHostPort(bind, strconv.Itoa(port)), suffix)}
}

func certLines(w func(int, string, ...any), indent int, domain string) {
	w(indent, "ssl_certificate %s/%s/fullchain.pem;", TokCerts, domain)
	w(indent, "ssl_certificate_key %s/%s/privkey.pem;", TokCerts, domain)
}

// decoyLocation serves a decoy site with clean URLs: /about is about.html or about/index.html.
func decoyLocation(w func(int, string, ...any), indent int, path, decoy string) {
	w(indent, "location %s {", locationPath(path))
	w(indent+1, "root %s/%s;", TokDecoy, decoy)
	w(indent+1, "index index.html;")
	w(indent+1, "try_files $uri $uri.html $uri/index.html =404;")
	w(indent, "}")
}

func errorPages(w func(int, string, ...any), indent int, decoy string) {
	w(indent, "error_page 404 /404.html;")
	// nginx's own error pages name nginx; a site whose backend is down or restarting shows its
	// usual missing page instead.
	w(indent, "error_page 403 500 502 503 504 =404 /404.html;")
	w(indent, "location = /404.html { root %s/%s; internal; }", TokDecoy, decoy)
}

func locationPath(prefix string) string {
	if prefix == "" || prefix == "/" {
		return "/"
	}
	return "^~ " + prefix
}

// locations renders the routes of one domain. Routes with a path prefix are matched as
// prefixes, the catch-all goes to "location /".
func locations(w func(int, string, ...any), rs []Route) error {
	decoy := ""
	seen := map[string]bool{}
	for _, r := range rs {
		p := r.PathPrefix
		if p != "" && !prefixRe.MatchString(p) {
			return fmt.Errorf("path %q is not a URL path", p)
		}
		if p == "/" {
			p = ""
		}
		if seen[p] {
			continue // the first route for a path wins, like the order of Caddy routes did
		}
		seen[p] = true
		switch r.Kind {
		case KindDecoy:
			if r.Decoy == "" {
				return fmt.Errorf("decoy route without a site")
			}
			if decoy == "" {
				decoy = r.Decoy
			}
			decoyLocation(w, 2, p, r.Decoy)
		case KindProxy:
			if err := checkAddr(r.Upstream); err != nil {
				return err
			}
			w(2, "location %s {", locationPath(p))
			w(3, "proxy_pass http://%s;", r.Upstream)
			proxyHeaders(w, 3)
			if r.InterceptErrors {
				w(3, "proxy_intercept_errors on;")
			}
			w(3, "client_max_body_size 16m;")
			w(2, "}")
		case KindStream:
			if err := checkAddr(r.Upstream); err != nil {
				return err
			}
			w(2, "location %s {", locationPath(p))
			w(3, "proxy_pass http://%s;", r.Upstream)
			proxyHeaders(w, 3)
			w(3, "proxy_buffering off;")
			w(3, "proxy_request_buffering off;")
			w(3, "proxy_read_timeout 1h;")
			w(3, "proxy_send_timeout 1h;")
			w(3, "client_max_body_size 0;")
			w(3, "client_body_timeout 1h;")
			w(2, "}")
		case KindRaw:
			if err := checkSnippet(r.Raw); err != nil {
				return fmt.Errorf("location %s: %w", locationPath(p), err)
			}
			w(2, "location %s {", locationPath(p))
			for _, line := range strings.Split(strings.TrimSpace(r.Raw), "\n") {
				w(3, "%s", strings.TrimRight(line, " \t"))
			}
			w(2, "}")
		default:
			return fmt.Errorf("unknown route kind %q", r.Kind)
		}
	}
	if !seen[""] {
		w(2, "location / { return 404; }")
	}
	if decoy != "" {
		errorPages(w, 2, decoy)
	}
	return nil
}

func proxyHeaders(w func(int, string, ...any), indent int) {
	w(indent, "proxy_set_header Host $host;")
	w(indent, "proxy_set_header X-Real-IP $remote_addr;")
	w(indent, "proxy_set_header X-Forwarded-For $remote_addr;")
	w(indent, "proxy_set_header X-Forwarded-Proto https;")
}

func checkAddr(a string) error {
	host, port, err := net.SplitHostPort(a)
	if err != nil || host == "" || strings.ContainsAny(a, " ;{}\"'\n") {
		return fmt.Errorf("upstream %q is not host:port", a)
	}
	if p, err := strconv.Atoi(port); err != nil || p <= 0 || p > 65535 {
		return fmt.Errorf("upstream %q: bad port", a)
	}
	return nil
}

// forbiddenNames are directives a template may not use: they load code, read or write files
// outside the generated config, or serve the server's own files.
var forbiddenNames = map[string]bool{
	"include": true, "load_module": true, "pid": true, "user": true, "daemon": true, "env": true,
	"error_log": true, "access_log": true, "root": true, "alias": true, "ssl_engine": true,
	"ssl_dhparam": true, "ssl_crl": true, "ssl_session_ticket_key": true,
	"proxy_store": true, "fastcgi_store": true, "uwsgi_store": true, "scgi_store": true,
}

var (
	forbiddenPrefix = []string{"perl", "lua", "js_", "njs"}
	// …_file, …_temp_path, …_certificate(_key), …_crl: they name files on the node.
	forbiddenSuffix = []string{"_file", "_temp_path", "_cache_path", "_certificate", "_certificate_key", "_crl"}
)

// forbiddenDirective tells why a directive may not appear in a template ("" = it may).
func forbiddenDirective(name string, args []string) string {
	n := strings.ToLower(name)
	if forbiddenNames[n] {
		return fmt.Sprintf("directive %q is not allowed in a template", name)
	}
	for _, p := range forbiddenPrefix {
		if strings.HasPrefix(n, p) {
			return fmt.Sprintf("directive %q is not allowed in a template", name)
		}
	}
	for _, x := range forbiddenSuffix {
		if strings.HasSuffix(n, x) {
			return fmt.Sprintf("directive %q is not allowed in a template", name)
		}
	}
	for _, a := range args {
		if strings.Contains(strings.ToLower(a), "unix:") {
			return fmt.Sprintf("%s: unix sockets are not allowed in a template", name)
		}
	}
	return ""
}

// checkSnippet accepts directives written in a template: balanced braces, no quotes left
// open, nothing that loads code or touches files outside the config. Every directive is
// checked, wherever it stands: "return 404; include x;" on one line is two directives.
func checkSnippet(s string) error {
	depth := 0
	inQuote := byte(0)
	var words []string
	var word strings.Builder
	inWord := false
	endWord := func() {
		if inWord {
			words = append(words, word.String())
			word.Reset()
			inWord = false
		}
	}
	endDirective := func() error {
		endWord()
		if len(words) > 0 {
			if why := forbiddenDirective(words[0], words[1:]); why != "" {
				return errors.New(why)
			}
		}
		words = words[:0]
		return nil
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inQuote != 0:
			switch c {
			case '\\':
				if i+1 < len(s) {
					i++
					word.WriteByte(s[i])
				}
			case inQuote:
				inQuote = 0
			default:
				word.WriteByte(c)
			}
		case c == '"' || c == '\'':
			inQuote, inWord = c, true
		case c == '#':
			for i < len(s) && s[i] != '\n' {
				i++
			}
			endWord()
		case c == '{':
			if err := endDirective(); err != nil {
				return err
			}
			depth++
		case c == '}':
			if err := endDirective(); err != nil {
				return err
			}
			depth--
			if depth < 0 {
				return fmt.Errorf("unbalanced }")
			}
		case c == ';':
			if err := endDirective(); err != nil {
				return err
			}
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			endWord()
		default:
			word.WriteByte(c)
			inWord = true
		}
	}
	if inQuote != 0 {
		return fmt.Errorf("unclosed quote")
	}
	if depth != 0 {
		return fmt.Errorf("unbalanced {")
	}
	// A last directive without its ';' is nginx's error to report, but it is still checked.
	return endDirective()
}

func oneLine(s string) string { return strings.NewReplacer("\n", " ", "\r", " ").Replace(s) }

func appendUnique(xs []string, x string) []string {
	for _, v := range xs {
		if v == x {
			return xs
		}
	}
	return append(xs, x)
}

var mimeTypes = []string{
	"text/html html htm;",
	"text/css css;",
	"text/plain txt;",
	"text/xml xml;",
	"application/javascript js mjs;",
	"application/json json;",
	"application/manifest+json webmanifest;",
	"image/svg+xml svg svgz;",
	"image/png png;",
	"image/jpeg jpg jpeg;",
	"image/gif gif;",
	"image/webp webp;",
	"image/avif avif;",
	"image/x-icon ico;",
	"font/woff2 woff2;",
	"font/woff woff;",
	"application/pdf pdf;",
}
