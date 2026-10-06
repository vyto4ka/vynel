package xray_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/vyto4ka/vpn/internal/xray"
	"github.com/vyto4ka/vpn/internal/xray/xraytest"
	"github.com/vyto4ka/vpn/internal/xrayconf"
)

// testBase lets traffic reach 127.0.0.1 (the default base blocks geoip:private).
const testBase = `{"log":{"loglevel":"warning"},"outbounds":[{"tag":"DIRECT","protocol":"freedom"}]}`

func TestTemplateBConfigAcceptedByXray(t *testing.T) {
	bin := xraytest.Binary(t)
	r, err := xrayconf.RenderInbound(xrayconf.InboundSpec{
		TemplateID:    "vless-xhttp-vkcdn",
		ProfileValues: map[string]any{"UPLINK_PATH": "/upload"},
		NodeValues:    map[string]any{"CDN_DOMAIN": "cdn.example.com"},
		Context:       xrayconf.NodeContext{Code: "NL", Domain: "origin.example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	base, _ := xrayconf.ParseBase(xrayconf.DefaultBaseJSON)
	cfg, err := xrayconf.BuildConfig(xrayconf.NodeConfig{
		Base: base, Inbounds: []*xrayconf.RenderedInbound{r}, APIAddr: "127.0.0.1:10085",
		Clients: map[string][]xrayconf.Client{r.Tag: {{Email: "1", ID: "11111111-1111-4111-8111-111111111111"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(cfg)
	if err := bin.Test(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
}

// End to end on a real Xray: template A with Reality, hot add user, traffic, stats, hot remove.
func TestRealityHotUsersAndStats(t *testing.T) {
	bin := xraytest.Binary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	dir := t.TempDir()

	const domain = "example.test"
	targetPort := xraytest.TLSTarget(t, domain)
	origin := xraytest.Origin(t)
	serverPort, apiPort := xraytest.FreePort(t), xraytest.FreePort(t)

	tpl, _ := xrayconf.GetTemplate("vless-reality-selfsteal")
	nodeVals, err := tpl.GenerateValues(xrayconf.ScopeNode, nil)
	if err != nil {
		t.Fatal(err)
	}
	r, err := xrayconf.RenderInbound(xrayconf.InboundSpec{
		TemplateID:    "vless-reality-selfsteal",
		ProfileValues: map[string]any{"SELFSTEAL_PORT": targetPort},
		NodeValues:    nodeVals,
		PortOverride:  serverPort,
		Context:       xrayconf.NodeContext{Code: "NL", Domain: domain, ListenIP: "127.0.0.1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	base, _ := xrayconf.ParseBase(testBase)
	apiAddr := "127.0.0.1:" + strconv.Itoa(apiPort)
	cfg, err := xrayconf.BuildConfig(xrayconf.NodeConfig{Base: base, Inbounds: []*xrayconf.RenderedInbound{r}, APIAddr: apiAddr})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(cfg)

	server := xray.NewProcess(bin, filepath.Join(dir, "server.json"), nil)
	defer server.Close()
	if err := server.Apply(ctx, raw); err != nil {
		t.Fatal(err)
	}
	api, err := xray.DialAPI(apiAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	if err := api.WaitReady(ctx); err != nil {
		t.Fatal(err, server.Tail())
	}

	uuid, _ := xrayconf.NewUUID()
	const email = "42"
	if err := api.AddVLESSUser(ctx, r.Tag, email, uuid, r.Flow); err != nil {
		t.Fatal(err)
	}
	if err := api.AddVLESSUser(ctx, r.Tag, email, uuid, r.Flow); err != nil {
		t.Fatalf("adding twice must be idempotent: %v", err)
	}
	users, err := api.InboundUsers(ctx, r.Tag)
	if err != nil || len(users) != 1 || users[0] != email {
		t.Fatalf("inbound users %v %v", users, err)
	}

	socksPort := xraytest.StartRealityClient(t, bin, xraytest.RealityClient{
		ServerPort: serverPort, UUID: uuid, Flow: r.Flow, ServerName: domain,
		PublicKey: r.Values["REALITY_PUBLIC_KEY"].(string), ShortID: r.Values["REALITY_SHORT_ID"].(string),
	})

	body, err := xraytest.Fetch(ctx, socksPort, origin)
	if err != nil {
		t.Fatalf("request through proxy failed: %v\nserver: %v", err, server.Tail())
	}
	if len(body) != xraytest.OriginSize {
		t.Fatalf("short body %d", len(body))
	}

	deltas, err := api.UserTrafficDeltas(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(deltas) != 1 || deltas[0].Email != email || deltas[0].Downlink < xraytest.OriginSize {
		t.Fatalf("stats: %+v", deltas)
	}
	again, _ := api.UserTrafficDeltas(ctx)
	if len(again) != 0 {
		t.Fatalf("stats must be reset after reading: %+v", again)
	}

	if err := api.RemoveUser(ctx, r.Tag, email); err != nil {
		t.Fatal(err)
	}
	if err := api.RemoveUser(ctx, r.Tag, email); err != nil {
		t.Fatalf("removing twice must be idempotent: %v", err)
	}
	if _, err := xraytest.Fetch(ctx, socksPort, origin); err == nil {
		t.Fatal("removed user can still connect")
	}
}
