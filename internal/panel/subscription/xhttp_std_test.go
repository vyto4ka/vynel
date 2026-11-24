package subscription

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
	"github.com/vyto4ka/vynel/internal/xray"
	"github.com/vyto4ka/vynel/internal/xray/xraytest"
)

// xhttpStdEnv is one node with template E (standard XHTTP + REALITY) and one user.
type xhttpStdEnv struct {
	*env
	node *store.Node
	ni   *store.NodeInbound
}

func newXHTTPStdEnv(t *testing.T, values map[string]any, port int) *xhttpStdEnv {
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
	prof, err := svc.CreateProfile(ctx, actor, service.ProfileInput{Name: "XHTTP std", TemplateID: "vless-xhttp-reality-std", Values: values})
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
	return &xhttpStdEnv{env: &env{ctx: ctx, svc: svc, h: NewHandler(svc, nil), user: u}, node: node, ni: ni}
}

// The link of the standard template matches the usual community config byte for byte:
// type=xhttp, path=/, mode=auto, extra={"mode":"auto","xPaddingBytes":"100-1342"}, fp=chrome.
func TestXHTTPStdLink(t *testing.T) {
	e := newXHTTPStdEnv(t, nil, 0)
	ls := links(t, e.get(e.path(), "Happ/3.1.0", "hw-1"))
	if len(ls) != 1 {
		t.Fatalf("links %v", ls)
	}
	l := ls[0]
	for _, want := range []string{"@example.test:443", "encryption=none", "type=xhttp", "path=%2F", "mode=auto",
		"extra=%7B%22mode%22%3A%22auto%22%2C%22xPaddingBytes%22%3A%22100-1342%22%7D",
		"security=reality", "sni=example.test", "fp=chrome", "pbk=", "sid="} {
		if !strings.Contains(l, want) {
			t.Fatalf("link lacks %q: %s", want, l)
		}
	}
	for _, nope := range []string{"flow=", "host="} {
		if strings.Contains(l, nope) {
			t.Fatalf("link has %q: %s", nope, l)
		}
	}
	// The client extra is the server extra: the template has no server-only keys.
	h, err := e.svc.HostFor(e.ctx, e.ni)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Extra) != 2 || h.Extra["mode"] != "auto" || h.Extra["xPaddingBytes"] != "100-1342" {
		t.Fatalf("client extra %v", h.Extra)
	}

	// Mihomo 1.19+ gets xhttp-opts (the template opts in).
	rec := e.get(e.path(), "clash-verge/v2.2.3", "hw-1")
	var doc struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(rec.Body.Bytes(), &doc); err != nil || len(doc.Proxies) != 1 {
		t.Fatalf("mihomo: %v %s", err, rec.Body.String())
	}
	p := doc.Proxies[0]
	xo, _ := p["xhttp-opts"].(map[string]any)
	if p["network"] != "xhttp" || xo == nil {
		t.Fatalf("mihomo proxy %v", p)
	}
	if xo["path"] != "/" || xo["mode"] != "auto" || xo["x-padding-bytes"] != "100-1342" {
		t.Fatalf("xhttp-opts %v", xo)
	}
}

// End to end on a real Xray: the node config for template E and the client config from the
// user's JSON subscription pass traffic in mode auto.
func TestXHTTPStdTrafficOnXray(t *testing.T) {
	bin := xraytest.Binary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	target := xraytest.TLSTarget(t, "example.test")
	origin := xraytest.Origin(t)
	serverPort, apiPort := xraytest.FreePort(t), xraytest.FreePort(t)
	e := newXHTTPStdEnv(t, map[string]any{"SELFSTEAL_PORT": target}, serverPort)
	if err := e.svc.SetSetting(ctx, actor, service.SettingXrayAPIPort, strconv.Itoa(apiPort)); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetHostOverride(ctx, actor, e.ni.ID, map[string]any{"address": "127.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	ds, err := e.svc.DesiredState(ctx, e.node.ID)
	if err != nil || len(ds.Problems) != 0 {
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
	xraytest.OpenFreedom(scfg)
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
	if xs["mode"] != "auto" || xs["path"] != "/" {
		t.Fatalf("client xhttp settings %v", xs)
	}
	raw, _ := json.Marshal(cfg)
	xraytest.StartClient(t, bin, raw, socks)

	xraytest.Eventually(t, 15*time.Second, "fetch through standard XHTTP + REALITY", func() error {
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
}
