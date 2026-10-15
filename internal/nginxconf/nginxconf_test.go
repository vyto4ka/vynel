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
	for _, bad := range []string{"include /etc/passwd;", "proxy_pass http://x;\n}", "location / {", "add_header X \"y;", "load_module x.so;"} {
		if checkSnippet(bad) == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	if err := checkSnippet("proxy_pass http://127.0.0.1:8085; # comment with { brace\nif ($request_method = POST) { return 405; }\nadd_header X \"a } b\";"); err != nil {
		t.Fatal(err)
	}
	_, err := Build(Spec{Sites: []Site{{Domain: "a.example.com", Routes: []Route{{Kind: KindRaw, PathPrefix: "/upload", Raw: "include x;"}}}}})
	if err == nil {
		t.Fatal("raw location with include accepted")
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
