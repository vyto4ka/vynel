package warp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vyto4ka/vynel/internal/geostore"
)

func TestKeyPairAndReserved(t *testing.T) {
	priv, pub, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	if pb, err := base64.StdEncoding.DecodeString(priv); err != nil || len(pb) != 32 {
		t.Fatalf("bad priv key: %q %v", priv, err)
	}
	if pb, err := base64.StdEncoding.DecodeString(pub); err != nil || len(pb) != 32 {
		t.Fatalf("bad pub key: %q %v", pub, err)
	}

	cid := base64.StdEncoding.EncodeToString([]byte{142, 18, 209})
	res, err := DecodeReserved(cid)
	if err != nil || len(res) != 3 || res[0] != 142 || res[1] != 18 || res[2] != 209 {
		t.Fatalf("unexpected reserved: %v %v", res, err)
	}
	if r2, err := ParseReserved("[142, 18, 209]"); err != nil || len(r2) != 3 || r2[0] != 142 {
		t.Fatalf("ParseReserved bracket: %v %v", r2, err)
	}
}

func TestProvisionAndLicense(t *testing.T) {
	cid := base64.StdEncoding.EncodeToString([]byte{10, 20, 30})
	licenseBound := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/reg":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":    "dev-1",
				"token": "tok-1",
				"account": map[string]any{
					"account_type": "free",
				},
				"config": map[string]any{
					"client_id": cid,
					"peers": []any{
						map[string]any{
							"public_key": DefaultPeerPublicKey,
							"endpoint":   map[string]any{"host": "engage.cloudflareclient.com:2408"},
						},
					},
					"interface": map[string]any{
						"addresses": map[string]any{
							"v4": "172.16.0.2",
							"v6": "2606:4700:110::1",
						},
					},
				},
			})
		case r.Method == http.MethodPut && r.URL.Path == "/reg/dev-1/account":
			licenseBound = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"account_type":"limited"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/reg/dev-1":
			accType := "free"
			if licenseBound {
				accType = "limited"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":      "dev-1",
				"token":   "tok-1",
				"account": map[string]any{"account_type": accType},
				"config": map[string]any{
					"client_id": cid,
					"interface": map[string]any{
						"addresses": map[string]any{"v4": "172.16.0.2", "v6": "2606:4700:110::1"},
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	creds, err := Provision(context.Background(), srv.Client(), srv.URL, "LIC-1234")
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if creds.AccountType != "limited" || creds.AddrV4 != "172.16.0.2/32" || len(creds.Reserved) != 3 || creds.Reserved[0] != 10 {
		t.Fatalf("unexpected creds: %+v", creds)
	}
}

func TestParseWireGuardConf(t *testing.T) {
	conf := `
[Interface]
PrivateKey = AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=
Address = 172.16.0.2/32, 2606:4700:110::1/128
MTU = 1280
# Reserved = [1, 2, 3]

[Peer]
PublicKey = bmXOC+F1FxEMF9dyiK2H5/1SUtzH0JuVo51h2wPfgyo=
Endpoint = 162.159.192.1:2408
PersistentKeepalive = 25
`
	c, err := ParseWireGuardConf(conf)
	if err != nil {
		t.Fatalf("ParseWireGuardConf: %v", err)
	}
	if c.CredMode != "manual" || c.AddrV4 != "172.16.0.2/32" || c.AddrV6 != "2606:4700:110::1/128" || len(c.Reserved) != 3 || c.Reserved[2] != 3 {
		t.Fatalf("unexpected parsed conf: %+v", c)
	}
}

func TestParseRules(t *testing.T) {
	parsed, err := ParseRules([]string{
		"# comment",
		"geosite:openai",
		"example.com",
		"full:api.example.org",
		"geoip:netflix",
		"104.28.0.0/16",
		"1.1.1.1",
		"ext-domain:custom.dat:ai@!cn",
		"ext-ip:custom-ip.dat:cf",
		"ext:auto-ip.dat:us",
	}, func(file string) geostore.Kind {
		if file == "auto-ip.dat" {
			return geostore.KindGeoIP
		}
		return geostore.KindGeoSite
	})
	if err != nil {
		t.Fatalf("ParseRules: %v", err)
	}
	if len(parsed.Domains) != 4 {
		t.Fatalf("expected 4 domains, got %v", parsed.Domains)
	}
	if len(parsed.IPs) != 5 {
		t.Fatalf("expected 5 ips, got %v", parsed.IPs)
	}
	if len(parsed.GeoRefs) != 5 {
		t.Fatalf("expected 5 georefs, got %+v", parsed.GeoRefs)
	}

	if _, err := ParseRules([]string{"geosite:"}, nil); err == nil {
		t.Fatal("expected error for empty geosite")
	}
	if _, err := ParseRules([]string{"ext-domain:../bad.dat:ai"}, nil); err == nil {
		t.Fatal("expected error for traversal filename")
	}
}
