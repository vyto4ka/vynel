package nginxconf

import (
	"strings"
	"testing"
)

func TestEmpty(t *testing.T) {
	if c, err := Build(Spec{}); err != nil || c != "" {
		t.Fatalf("%q %v", c, err)
	}
}

func TestBuildMergesRoutesOfOneDomain(t *testing.T) {
	c, err := Build(Spec{Email: "admin@example.com", Sites: []Site{
		{Domain: "Sub.Example.com", Routes: []Route{{Kind: KindProxy, PathPrefix: "/s/", Upstream: "127.0.0.1:2096"}}},
		{Domain: "sub.example.com", Routes: []Route{{Kind: KindDecoy, Decoy: "docs"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(c, "server_name sub.example.com;") != 1 {
		t.Fatalf("one server block per domain:\n%s", c)
	}
	for _, want := range []string{"location ^~ /s/ {", "proxy_pass http://127.0.0.1:2096;", "root @@DECOY@@/docs;",
		"try_files $uri $uri.html $uri/index.html =404;", "listen 443 ssl http2;", "#ipv6#listen [::]:443 ssl http2;",
		"listen 443 ssl http2 default_server;", "Strict-Transport-Security", "ssl_protocols TLSv1.2 TLSv1.3;",
		"return 301 https://$host$request_uri;", "root @@ACME@@;"} {
		if !strings.Contains(c, want) {
			t.Fatalf("missing %q:\n%s", want, c)
		}
	}
	if e, i := ACME(c); e != "admin@example.com" || i != "acme" {
		t.Fatalf("meta %q %q", e, i)
	}
	if d := CertDomains(c); len(d) != 1 || d[0] != "sub.example.com" {
		t.Fatalf("cert domains %v", d)
	}
}

func TestNamesAndConflicts(t *testing.T) {
	_, err := Build(Spec{Sites: []Site{
		{Domain: "a.example.com", Names: []string{"cdn.example.com"}, Routes: []Route{{Kind: KindDecoy, Decoy: "cloud"}}},
		{Domain: "b.example.com", Names: []string{"cdn.example.com"}, Routes: []Route{{Kind: KindDecoy, Decoy: "cloud"}}},
	}})
	if err == nil || !strings.Contains(err.Error(), "cdn.example.com") {
		t.Fatalf("one name on two sites of one listener: %v", err)
	}
	c, err := Build(Spec{Sites: []Site{
		{Domain: "a.example.com", Names: []string{"CDN.example.com"}, LocalPort: 8443, ProxyProtocol: true,
			Routes: []Route{{Kind: KindDecoy, Decoy: "cloud"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c, "server_name a.example.com cdn.example.com;") || !strings.Contains(c, "listen 127.0.0.1:8443 ssl proxy_protocol http2;") {
		t.Fatalf("names / local listener:\n%s", c)
	}
	if d := CertDomains(c); len(d) != 1 {
		t.Fatalf("extra names get no certificate: %v", d)
	}
	if l := PublicListeners(c); len(l) != 1 || l[0].Port != 80 {
		t.Fatalf("only :80 is public here: %v", l)
	}
}

func TestSnippets(t *testing.T) {
	for _, bad := range []string{"include /etc/passwd;", "proxy_pass http://x;\n}", "location / {", "add_header X \"y;", "load_module x.so;",
		// Every directive counts, not only the first on a line.
		"return 404; include /dev/null;", "add_header A b; error_log /etc/ld.so.preload;", "if ($x) { include y; }",
		"\"include\" /etc/passwd;", "INCLUDE /etc/passwd;",
		// Serving or writing the node's own files.
		"root /etc;", "alias /etc/;", "access_log /etc/cron.d/x;", "auth_basic_user_file /etc/shadow;",
		"client_body_temp_path /etc;", "proxy_store on;", "ssl_trusted_certificate /etc/x.pem;", "js_import x.js;",
		// Local unix sockets (the Docker API and the like), backslash escapes and internal Xray API.
		"proxy_pass http://unix:/var/run/docker.sock:/;", "grpc_pass unix:/run/x.sock;",
		`\include /etc/passwd;`, `al\ias /etc/;`, `proxy_pass http://u\nix:/run/x.sock:/;`,
		"proxy_pass http://127.0.0.1:10085;", "grpc_pass grpc://127.0.0.1:10085;"} {
		if checkSnippet(bad) == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	// What real templates write passes.
	for _, good := range []string{
		"proxy_pass http://127.0.0.1:8085;\nproxy_http_version 1.1;\nproxy_request_buffering off;\nproxy_buffering off;\nclient_max_body_size 0;\nproxy_read_timeout 3600s;\nproxy_set_header Host $host;\nproxy_set_header Connection \"\";",
		"large_client_header_buffers 16 64k;\nclient_header_buffer_size 4k;",
		"if ($request_method !~ ^(GET|HEAD)$) { return 405; }",
		"add_header X-Test 'a; b' always;",
	} {
		if err := checkSnippet(good); err != nil {
			t.Fatalf("rejected %q: %v", good, err)
		}
	}
	if err := checkSnippet("proxy_pass http://127.0.0.1:8085; # comment with { brace\nif ($request_method = POST) { return 405; }\nadd_header X \"a } b\";"); err != nil {
		t.Fatal(err)
	}
	_, err := Build(Spec{Sites: []Site{{Domain: "a.example.com", Routes: []Route{{Kind: KindRaw, PathPrefix: "/upload", Raw: "include x;"}}}}})
	if err == nil {
		t.Fatal("raw location with include accepted")
	}
	if _, err := Build(Spec{Sites: []Site{{Domain: "a.example.com", Routes: []Route{{Kind: KindDecoy, Decoy: "../etc"}}}}}); err == nil {
		t.Fatal("path traversal decoy accepted")
	}
}

func TestStreamAndFinalize(t *testing.T) {
	c, err := Build(Spec{
		Streams: []Stream{{Port: 443, Backends: []Backend{{Names: []string{"nl.example.com"}, Addr: "127.0.0.1:10443"}}, Default: "127.0.0.1:8443"}},
		Sites:   []Site{{Domain: "origin.example.com", LocalPort: 8443, ProxyProtocol: true, Routes: []Route{{Kind: KindDecoy, Decoy: "cloud"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ssl_preread on;", "nl.example.com 127.0.0.1:10443;", "default 127.0.0.1:8443;", "proxy_protocol on;"} {
		if !strings.Contains(c, want) {
			t.Fatalf("missing %q:\n%s", want, c)
		}
	}
	l := PublicListeners(c)
	if len(l) != 2 {
		t.Fatalf("public: 443 and 80, got %v", l)
	}
	f := Finalize(c, Local{Modules: []string{"/usr/lib/nginx/modules/ngx_stream_module.so"}, User: "www-data", Run: "/run/x", Certs: "/c", Decoy: "/d", ACME: "/a"})
	if strings.Contains(f, "@@") || !strings.Contains(f, "# listen [::]:443") || !strings.Contains(f, "load_module /usr/lib/nginx/modules/ngx_stream_module.so;") {
		t.Fatalf("finalize:\n%s", f)
	}
	if v6 := Finalize(c, Local{IPv6: true}); !strings.Contains(v6, " listen [::]:443 reuseport;") || strings.Contains(v6, "# listen") {
		t.Fatal("IPv6 listen not enabled")
	}
}

func TestHTTP2ModesAndNginx2RootSnippetHardening(t *testing.T) {
	c, err := Build(Spec{Sites: []Site{{Domain: "a.example.com", Routes: []Route{{Kind: KindDecoy, Decoy: "cloud"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	noH2 := Finalize(c, Local{NoHTTP2: true, IPv6: true, RejectHandshake: true})
	if strings.Contains(noH2, "http2") {
		t.Fatalf("NoHTTP2 left http2 active:\n%s", noH2)
	}
	modH2 := Finalize(c, Local{ModernHTTP2: true, IPv6: true, RejectHandshake: true})
	if strings.Contains(modH2, "ssl http2") || strings.Count(modH2, "http2 on;") != 2 {
		t.Fatalf("ModernHTTP2 expected two http2 on; (default_server + site) and no listen ... http2:\n%s", modH2)
	}
	for _, bad := range []string{
		"resolver 1.1.1.1;",
		"resolver_timeout 5s;",
		"mirror /sub;",
		"ssi on;",
		"ssi_types text/html;",
		"auth_request /auth;",
		"addition_before_body /hdr;",
		"listen 8443 ssl http2;",
		"http2 on;",
		"http2_chunk_size 8k;",
		"http3 on;",
		"quic_retry on;",
		"mail { }",
		"auth_http 127.0.0.1:9000;",
	} {
		if checkSnippet(bad) == nil {
			t.Fatalf("NGINX2Root vector accepted in snippet: %q", bad)
		}
	}
}

func TestCustomDecoyPath(t *testing.T) {
	c, err := Build(Spec{Sites: []Site{
		{Domain: "dir.example.com", Routes: []Route{{Kind: KindDecoy, DecoyPath: "/var/www/custom-site"}}},
		{Domain: "file.example.com", Routes: []Route{{Kind: KindDecoy, DecoyPath: "/var/www/stubs/landing.html"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"root /var/www/custom-site;",
		"location = /404.html { root /var/www/custom-site; try_files /404.html /index.html =404; internal; }",
		"root /var/www/stubs;",
		"try_files /landing.html =404;",
		"location = /404.html { root /var/www/stubs; try_files /landing.html =404; internal; }",
	} {
		if !strings.Contains(c, want) {
			t.Fatalf("missing %q in:\n%s", want, c)
		}
	}
	for _, bad := range []string{"../etc", "/etc/passwd", "/var/www/a;b", "relative/dir"} {
		if _, err := Build(Spec{Sites: []Site{{Domain: "bad.example.com", Routes: []Route{{Kind: KindDecoy, DecoyPath: bad}}}}}); err == nil {
			t.Fatalf("bad DecoyPath %q was accepted", bad)
		}
	}
}

