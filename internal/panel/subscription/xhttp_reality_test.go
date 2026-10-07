package subscription

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
	"github.com/vyto4ka/vynel/internal/xray"
	"github.com/vyto4ka/vynel/internal/xray/xraytest"
)

// xhttpRealityEnv is one node with template C (XHTTP + REALITY) and one user.
type xhttpRealityEnv struct {
	*env
	node *store.Node
	ni   *store.NodeInbound
}

func newXHTTPRealityEnv(t *testing.T, values map[string]any, port int) *xhttpRealityEnv {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	svc := service.New(st)
	if err := svc.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	node, _, err := svc.CreateNode(ctx, actor, service.NodeInput{Name: "Нидерланды", Country: "nl", Domain: "example.test"})
	if err != nil {
		t.Fatal(err)
	}
	prof, err := svc.CreateProfile(ctx, actor, service.ProfileInput{Name: "XHTTP", TemplateID: "vless-xhttp-reality", Values: values})
	if err != nil {
		t.Fatal(err)
	}
	main, _ := svc.GroupByName(ctx, "Основная")
	if err := svc.GrantAccess(ctx, actor, main.ID, store.AccessProfile, prof.ID); err != nil {
		t.Fatal(err)
	}
	ni, err := svc.AttachProfile(ctx, actor, service.AttachInput{NodeID: node.ID, ProfileID: prof.ID, PortOverride: port})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SetSetting(ctx, actor, service.SettingSubDomain, "sub.example.com"); err != nil {
		t.Fatal(err)
	}
	u, err := svc.CreateUser(ctx, actor, service.CreateUserInput{Username: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	return &xhttpRealityEnv{env: &env{ctx: ctx, svc: svc, h: NewHandler(svc, nil), user: u}, node: node, ni: ni}
}

func TestXHTTPRealityLinkAndMihomo(t *testing.T) {
	e := newXHTTPRealityEnv(t, map[string]any{"XHTTP_PATH": "/assets/sync", "XHTTP_OBFS": "cookie", "FINGERPRINT": "safari"}, 0)
	ls := links(t, e.get(e.path(), "Happ/3.1.0", "hw-1"))
	if len(ls) != 1 {
		t.Fatalf("links %v", ls)
	}
	l := ls[0]
	for _, want := range []string{"@example.test:443", "type=xhttp", "security=reality", "sni=example.test", "host=example.test",
		"fp=safari", "pbk=", "sid=", "path=%2Fassets%2Fsync%2F", "mode=stream-up", "extra="} {
		if !strings.Contains(l, want) {
			t.Fatalf("link lacks %q: %s", want, l)
		}
	}
	if strings.Contains(l, "flow=") {
		t.Fatalf("Vision over XHTTP: %s", l)
	}
	// The client extra is the template's complete client object: no server-only keys.
	h, err := e.svc.HostFor(e.ctx, e.ni)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"noSSEHeader", "serverMaxHeaderBytes"} {
		if _, ok := h.Extra[k]; ok {
			t.Fatalf("server-only %s in the client extra: %v", k, h.Extra)
		}
	}
	if h.Extra["uplinkDataPlacement"] != "body" || h.Extra["mode"] != "stream-up" || h.Extra["xmux"] == nil {
		t.Fatalf("client extra %v", h.Extra)
	}

	rec := e.get(e.path(), "clash-verge/v2.2.3", "hw-1")
	var doc struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(rec.Body.Bytes(), &doc); err != nil || len(doc.Proxies) != 1 {
		t.Fatalf("mihomo: %v %s", err, rec.Body.String())
	}
	p := doc.Proxies[0]
	ro, _ := p["reality-opts"].(map[string]any)
	xo, _ := p["xhttp-opts"].(map[string]any)
	if p["network"] != "xhttp" || ro["support-x25519mlkem768"] != true || xo == nil {
		t.Fatalf("mihomo proxy %v", p)
	}
	want := map[string]any{"path": "/assets/sync/", "host": "example.test", "mode": "stream-up", "session-placement": "cookie",
		"session-key": "media_sid", "seq-placement": "query", "seq-key": "offset", "x-padding-obfs-mode": true,
		"x-padding-placement": "query", "x-padding-key": "cb", "x-padding-method": "repeat-x", "x-padding-bytes": "100-1000",
		"uplink-http-method": "POST", "uplink-data-placement": "body", "no-grpc-header": true}
	for k, v := range want {
		if xo[k] != v {
			t.Fatalf("xhttp-opts %s = %v, want %v (%v)", k, xo[k], v, xo)
		}
	}
	rs, _ := xo["reuse-settings"].(map[string]any)
	if rs["max-concurrency"] != "4-8" || rs["h-max-reusable-secs"] != "600-1800" {
		t.Fatalf("reuse-settings %v", rs)
	}

	// sing-box has no XHTTP: the user gets a stub instead of a broken outbound.
	rec = e.get(e.path(), "sing-box 1.11.4", "hw-1")
	if strings.Contains(rec.Body.String(), "example.test") {
		t.Fatalf("sing-box got the XHTTP host: %s", rec.Body.String())
	}
}

// End to end on a real Xray: the node config the panel builds for template C, and the client
// config from the user's JSON subscription, pass traffic in both modes.
func TestXHTTPRealityTrafficOnXray(t *testing.T) {
	bin := xraytest.Binary(t)
	// "old core": a client that does not know the 2026 placement keys and silently drops them.
	newKeys := []string{"sessionIDPlacement", "sessionIDKey", "sessionPlacement", "sessionKey", "seqPlacement", "seqKey",
		"xPaddingObfsMode", "xPaddingPlacement", "xPaddingKey", "xPaddingMethod", "uplinkDataPlacement", "uplinkHTTPMethod"}
	for _, c := range []struct {
		mode, obfs string
		oldCore    bool
	}{
		{"stream-up", "compat", false}, {"packet-up", "compat", false},
		{"stream-up", "cookie", false}, {"packet-up", "cookie", false},
		{"stream-up", "compat", true}, {"packet-up", "compat", true},
	} {
		mode := c.mode
		name := c.mode + "/" + c.obfs
		if c.oldCore {
			name += "/old-core"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			target := xraytest.TLSTarget(t, "example.test")
			origin := xraytest.Origin(t)
			serverPort, apiPort := xraytest.FreePort(t), xraytest.FreePort(t)
			e := newXHTTPRealityEnv(t, map[string]any{"XHTTP_MODE": mode, "XHTTP_OBFS": c.obfs, "SELFSTEAL_PORT": target}, serverPort)
			if err := e.svc.SetSetting(ctx, actor, service.SettingXrayAPIPort, strconv.Itoa(apiPort)); err != nil {
				t.Fatal(err)
			}
			if err := e.svc.SetHostOverride(ctx, actor, e.ni.ID, map[string]any{"address": "127.0.0.1"}); err != nil {
				t.Fatal(err)
			}
			ds, err := e.svc.DesiredState(ctx, e.node.ID)
			if err != nil || len(ds.Problems) > 0 {
				t.Fatalf("desired state: %v %v", err, ds.Problems)
			}
			server := xray.NewProcess(bin, filepath.Join(t.TempDir(), "server.json"), nil)
			defer server.Close()
			defer func() {
				if t.Failed() {
					t.Logf("xray server: %v", server.Tail())
				}
			}()
			var scfg map[string]any
			_ = json.Unmarshal(ds.Config, &scfg)
			// The base config blocks private IPs; the test origin is on 127.0.0.1.
			if rt, ok := scfg["routing"].(map[string]any); ok {
				var keep []any
				for _, r := range rt["rules"].([]any) {
					if b, _ := json.Marshal(r); !strings.Contains(string(b), "geoip:private") {
						keep = append(keep, r)
					}
				}
				rt["rules"] = keep
			}
			if os.Getenv("XHTTP_DEBUG") != "" {
				scfg["log"] = map[string]any{"loglevel": "debug"}
			}
			sraw, _ := json.Marshal(scfg)
			if err := server.Apply(ctx, sraw); err != nil {
				t.Fatal(err)
			}
			// Users go in through the API, as the node agent does.
			api, err := xray.DialAPI("127.0.0.1:" + strconv.Itoa(apiPort))
			if err != nil {
				t.Fatal(err)
			}
			defer api.Close()
			if err := api.WaitReady(ctx); err != nil {
				t.Fatal(err)
			}
			for _, in := range ds.Inbounds {
				for _, c := range ds.Users[in.Tag] {
					if err := api.AddVLESSUser(ctx, in.Tag, c.Email, c.ID, in.Flow); err != nil {
						t.Fatal(err)
					}
				}
			}

			var configs []map[string]any
			if err := json.Unmarshal(e.get(e.path()+"/json", "x", "hw-1").Body.Bytes(), &configs); err != nil || len(configs) != 1 {
				t.Fatalf("json subscription: %v", err)
			}
			cfg := configs[0]
			socks := xraytest.FreePort(t)
			in := cfg["inbounds"].([]any)[0].(map[string]any)
			in["port"] = socks
			cfg["inbounds"] = []any{in}
			delete(cfg, "dns")
			delete(cfg, "routing") // geoip:private -> direct would bypass the proxy for the local origin
			xs := cfg["outbounds"].([]any)[0].(map[string]any)["streamSettings"].(map[string]any)["xhttpSettings"].(map[string]any)
			if xs["mode"] != mode {
				t.Fatalf("client mode %v", xs["mode"])
			}
			if c.oldCore {
				extra := xs["extra"].(map[string]any)
				for _, k := range newKeys {
					delete(extra, k)
				}
			}
			raw, _ := json.Marshal(cfg)
			xraytest.StartClient(t, bin, raw, socks)

			xraytest.Eventually(t, 15*time.Second, "fetch through XHTTP + REALITY", func() error {
				body, err := xraytest.Fetch(ctx, socks, origin)
				if err == nil && len(body) != xraytest.OriginSize {
					return errShort(len(body))
				}
				return err
			})
			// The bytes must have crossed the node, not a direct route.
			deltas, err := api.UserTrafficDeltas(ctx)
			if err != nil || len(deltas) != 1 || deltas[0].Downlink < xraytest.OriginSize {
				t.Fatalf("traffic did not go through the inbound: %+v %v", deltas, err)
			}

			if os.Getenv("MIHOMO_BIN") != "" && !c.oldCore {
				socks := startMihomo(t, e.get(e.path(), "clash-verge/v2.2.3", "hw-1").Body.Bytes())
				xraytest.Eventually(t, 15*time.Second, "fetch through Mihomo", func() error {
					body, err := xraytest.Fetch(ctx, socks, origin)
					if err == nil && len(body) != xraytest.OriginSize {
						return errShort(len(body))
					}
					return err
				})
				if deltas, err := api.UserTrafficDeltas(ctx); err != nil || len(deltas) != 1 || deltas[0].Downlink < xraytest.OriginSize {
					t.Fatalf("Mihomo traffic did not go through the inbound: %+v %v", deltas, err)
				}
			}
		})
	}
}

// startMihomo runs MIHOMO_BIN with the subscription profile (mixed port moved, geo rules dropped
// so it needs no downloads) and returns its SOCKS port.
func startMihomo(t *testing.T, profile []byte) int {
	t.Helper()
	var doc map[string]any
	if err := yaml.Unmarshal(profile, &doc); err != nil {
		t.Fatal(err)
	}
	port := xraytest.FreePort(t)
	doc["mixed-port"] = port
	doc["rules"] = []string{"MATCH,VPN"}
	doc["geodata-mode"] = false
	dir := t.TempDir()
	raw, _ := yaml.Marshal(doc)
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Getenv("MIHOMO_BIN"), "-d", dir)
	out := &lockedBuffer{}
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if t.Failed() {
			t.Logf("mihomo:\n%s", out.String())
		}
	})
	xraytest.Eventually(t, 10*time.Second, "mihomo listening", func() error {
		c, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port))
		if err != nil {
			return fmt.Errorf("%w\n%s", err, out.String())
		}
		return c.Close()
	})
	return port
}

type errShort int

func (n errShort) Error() string { return "short body " + strconv.Itoa(int(n)) }

func TestHysteria2Formats(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	svc := service.New(st)
	if err := svc.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	node, _, _ := svc.CreateNode(ctx, actor, service.NodeInput{Name: "Нидерланды", Country: "nl", Domain: "nl.example.com"})
	prof, err := svc.CreateProfile(ctx, actor, service.ProfileInput{Name: "HY2", TemplateID: "hysteria2"})
	if err != nil {
		t.Fatal(err)
	}
	main, _ := svc.GroupByName(ctx, "Основная")
	_ = svc.GrantAccess(ctx, actor, main.ID, store.AccessProfile, prof.ID)
	if _, err := svc.AttachProfile(ctx, actor, service.AttachInput{NodeID: node.ID, ProfileID: prof.ID}); err != nil {
		t.Fatal(err)
	}
	_ = svc.SetSetting(ctx, actor, service.SettingSubDomain, "sub.example.com")
	u, _ := svc.CreateUser(ctx, actor, service.CreateUserInput{Username: "alice"})
	e := &env{ctx: ctx, svc: svc, h: NewHandler(svc, nil), user: u}

	ls := links(t, e.get(e.path(), "Happ/3.1.0", "hw-1"))
	if len(ls) != 1 || !strings.HasPrefix(ls[0], "hysteria2://"+u.UUID+"@nl.example.com:443/?") ||
		!strings.Contains(ls[0], "sni=nl.example.com") || !strings.Contains(ls[0], "alpn=h3") || !strings.Contains(ls[0], "#%F0%9F%87%B3%F0%9F%87%B1") {
		t.Fatalf("links %v", ls)
	}
	var doc struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(e.get(e.path(), "clash-verge/v2.2.3", "hw-1").Body.Bytes(), &doc); err != nil || len(doc.Proxies) != 1 {
		t.Fatalf("mihomo: %v", err)
	}
	if p := doc.Proxies[0]; p["type"] != "hysteria2" || p["password"] != u.UUID || p["sni"] != "nl.example.com" {
		t.Fatalf("mihomo proxy %v", p)
	}
	var sb struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	if err := json.Unmarshal(e.get(e.path(), "sing-box 1.11.4", "hw-1").Body.Bytes(), &sb); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, o := range sb.Outbounds {
		if o["type"] == "hysteria2" && o["password"] == u.UUID && o["server_port"] == float64(443) {
			found = true
		}
	}
	if !found {
		t.Fatalf("sing-box outbounds %v", sb.Outbounds)
	}
}

// lockedBuffer collects a child process's output while the test reads it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
