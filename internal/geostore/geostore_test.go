package geostore

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
)

func encodeDomain(typ uint64, val string, attrs ...string) []byte {
	var b []byte
	if typ != 0 {
		b = protowire.AppendTag(b, 1, protowire.VarintType)
		b = protowire.AppendVarint(b, typ)
	}
	b = protowire.AppendTag(b, 2, protowire.BytesType)
	b = protowire.AppendString(b, val)
	for _, a := range attrs {
		var ab []byte
		ab = protowire.AppendTag(ab, 1, protowire.BytesType)
		ab = protowire.AppendString(ab, a)
		b = protowire.AppendTag(b, 3, protowire.BytesType)
		b = protowire.AppendBytes(b, ab)
	}
	return b
}

func encodeGeoSiteEntry(code string, domains ...[]byte) []byte {
	var b []byte
	b = protowire.AppendTag(b, 1, protowire.BytesType)
	b = protowire.AppendString(b, code)
	for _, d := range domains {
		b = protowire.AppendTag(b, 2, protowire.BytesType)
		b = protowire.AppendBytes(b, d)
	}
	return b
}

func encodeCIDR(ip net.IP, prefix uint32) []byte {
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	var b []byte
	b = protowire.AppendTag(b, 1, protowire.BytesType)
	b = protowire.AppendBytes(b, ip)
	b = protowire.AppendTag(b, 2, protowire.VarintType)
	b = protowire.AppendVarint(b, uint64(prefix))
	return b
}

func encodeGeoIPEntry(code string, cidrs ...[]byte) []byte {
	var b []byte
	b = protowire.AppendTag(b, 1, protowire.BytesType)
	b = protowire.AppendString(b, code)
	for _, c := range cidrs {
		b = protowire.AppendTag(b, 2, protowire.BytesType)
		b = protowire.AppendBytes(b, c)
	}
	return b
}

func encodeList(entries ...[]byte) []byte {
	var b []byte
	for _, e := range entries {
		b = protowire.AppendTag(b, 1, protowire.BytesType)
		b = protowire.AppendBytes(b, e)
	}
	return b
}

func buildTestGeoSite() []byte {
	return encodeList(
		encodeGeoSiteEntry("OPENAI",
			encodeDomain(domainRootDomain, "openai.com"),
			encodeDomain(domainRootDomain, "chatgpt.com"),
			encodeDomain(domainFull, "cdn.openai.com", "cdn"),
			encodeDomain(domainRegex, `^chatgpt-async-.*\.example\.com$`),
		),
		encodeGeoSiteEntry("GOOGLE",
			encodeDomain(domainRootDomain, "google.com"),
			encodeDomain(domainRootDomain, "googleadservices.com", "ads"),
			encodeDomain(domainPlain, "gstatic"),
		),
	)
}

func buildTestGeoIP() []byte {
	return encodeList(
		encodeGeoIPEntry("CLOUDFLARE",
			encodeCIDR(net.ParseIP("104.28.0.0"), 16),
			encodeCIDR(net.ParseIP("2606:4700::"), 32),
		),
		encodeGeoIPEntry("RU",
			encodeCIDR(net.ParseIP("77.88.8.0"), 24),
		),
	)
}

func TestIndexInspectAndSearch(t *testing.T) {
	dir := t.TempDir()
	sitePath := filepath.Join(dir, "geosite.dat")
	ipPath := filepath.Join(dir, "geoip.dat")
	if err := os.WriteFile(sitePath, buildTestGeoSite(), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ipPath, buildTestGeoIP(), 0o600); err != nil {
		t.Fatal(err)
	}

	// Auto-detect GeoSite.
	sIdx, err := IndexFile(sitePath, "")
	if err != nil {
		t.Fatalf("IndexFile geosite: %v", err)
	}
	if sIdx.Kind != KindGeoSite || len(sIdx.Categories) != 2 || sIdx.EntriesCount != 7 {
		t.Fatalf("unexpected geosite index: %+v", sIdx)
	}

	// Inspect with attribute filter.
	det, err := InspectCategory("geosite.dat", sitePath, sIdx, "google", "", "ads", 0, 50)
	if err != nil {
		t.Fatalf("InspectCategory: %v", err)
	}
	if det.Total != 3 || det.Filtered != 1 || len(det.Domains) != 1 || det.Domains[0].Value != "googleadservices.com" {
		t.Fatalf("unexpected inspect details: %+v", det)
	}

	// Content search by domain.
	hits, err := SearchContent(sitePath, sIdx, "api.chatgpt.com", 10)
	if err != nil {
		t.Fatalf("SearchContent domain: %v", err)
	}
	if len(hits) != 1 || hits[0].Code != "openai" {
		t.Fatalf("expected openai hit, got %+v", hits)
	}

	// Auto-detect GeoIP.
	ipIdx, err := IndexFile(ipPath, "")
	if err != nil {
		t.Fatalf("IndexFile geoip: %v", err)
	}
	if ipIdx.Kind != KindGeoIP || len(ipIdx.Categories) != 2 || ipIdx.EntriesCount != 3 {
		t.Fatalf("unexpected geoip index: %+v", ipIdx)
	}

	// Content search by IP inside CIDR.
	ipHits, err := SearchContent(ipPath, ipIdx, "104.28.15.9", 10)
	if err != nil {
		t.Fatalf("SearchContent ip: %v", err)
	}
	if len(ipHits) != 1 || ipHits[0].Code != "cloudflare" {
		t.Fatalf("expected cloudflare hit, got %+v", ipHits)
	}
}

func TestIndexRejectsBadFiles(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"zip", []byte("PK\x03\x04dummy"), "ZIP"},
		{"html", []byte("<!DOCTYPE html><html>404</html>"), "HTML"},
		{"json", []byte(`{"error": "not found"}`), "JSON"},
		{"empty", nil, "пустой"},
		{"garbage", []byte{0x0a, 0x80, 0x80, 0x80}, "protobuf"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), tc.name+".dat")
			if err := os.WriteFile(p, tc.data, 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := IndexFile(p, "")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestStoreDownloadAndFallback(t *testing.T) {
	siteBytes := buildTestGeoSite()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok.dat":
			_, _ = w.Write(siteBytes)
		case "/bad.html":
			_, _ = w.Write([]byte("<html>nope</html>"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	sysDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(sysDir, SystemGeoIP), buildTestGeoIP(), 0o600); err != nil {
		t.Fatal(err)
	}
	st := New(filepath.Join(t.TempDir(), "geo"), sysDir, srv.Client())
	if err := st.EnsureTesterDir(); err != nil {
		t.Fatal(err)
	}

	res, err := st.Download(context.Background(), srv.URL+"/ok.dat", "custom-site.dat", "", nil)
	if err != nil {
		t.Fatalf("Download ok: %v", err)
	}
	if res.Kind != KindGeoSite || res.CategoriesCount != 2 || res.SHA256 == "" {
		t.Fatalf("unexpected download result: %+v", res)
	}

	// Reject HTML download.
	if _, err := st.Download(context.Background(), srv.URL+"/bad.html", "bad.dat", "", nil); err == nil {
		t.Fatal("expected error on HTML response")
	}

	// Search across custom + system files.
	hits := st.SearchNames([]string{"custom-site.dat", SystemGeoIP}, "open", "", 20)
	if len(hits) != 1 || hits[0].Rule != "ext-domain:custom-site.dat:openai" {
		t.Fatalf("unexpected search hits: %+v", hits)
	}
	ipHits := st.SearchNames([]string{"custom-site.dat", SystemGeoIP}, "cloud", KindGeoIP, 20)
	if len(ipHits) != 1 || ipHits[0].Rule != "geoip:cloudflare" || !ipHits[0].IsSystem {
		t.Fatalf("unexpected geoip hits: %+v", ipHits)
	}
}

func TestValidateFileName(t *testing.T) {
	if got, err := ValidateFileName("runetfreedom-geosite"); err != nil || got != "runetfreedom-geosite.dat" {
		t.Fatalf("unexpected: %q %v", got, err)
	}
	for _, bad := range []string{"", "../etc/passwd", "a/b.dat", "файл.dat", ".hidden.dat"} {
		if _, err := ValidateFileName(bad); err == nil {
			t.Fatalf("expected error for %q", bad)
		}
	}
}
