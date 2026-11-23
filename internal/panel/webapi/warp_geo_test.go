package webapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"

	"github.com/vyto4ka/vynel/internal/geostore"
	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
	"github.com/vyto4ka/vynel/internal/warp"
)

func buildAPITestGeoSite() []byte {
	var d []byte
	d = protowire.AppendTag(d, 1, protowire.VarintType)
	d = protowire.AppendVarint(d, 2)
	d = protowire.AppendTag(d, 2, protowire.BytesType)
	d = protowire.AppendString(d, "openai.com")
	var gs []byte
	gs = protowire.AppendTag(gs, 1, protowire.BytesType)
	gs = protowire.AppendString(gs, "OPENAI")
	gs = protowire.AppendTag(gs, 2, protowire.BytesType)
	gs = protowire.AppendBytes(gs, d)
	var list []byte
	list = protowire.AppendTag(list, 1, protowire.BytesType)
	list = protowire.AppendBytes(list, gs)
	return list
}

func TestWarpAndGeoAPI(t *testing.T) {
	c, svc, pw := newServer(t)
	if w := c.do("POST", "/api/login", `{"login":"boss","password":"`+pw+`"}`, true); w.Code != 200 {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	ctx := context.Background()
	n, _, err := svc.CreateNode(ctx, service.ActorCLI, service.NodeInput{Code: "nl", Name: "NL", Country: "nl"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetNodeFacts(ctx, svc.Store().DB, n.ID, store.NodeFacts{Capabilities: []string{service.CapGeo, service.CapWarp}}); err != nil {
		t.Fatal(err)
	}

	dat := buildAPITestGeoSite()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(dat)
	}))
	defer srv.Close()

	svc.Geo = geostore.New(filepath.Join(t.TempDir(), "geo"), "", srv.Client())
	svc.WarpProvision = func(_ context.Context, _ int64, _ string, _ *warp.Credentials) (*warp.Credentials, error) {
		return &warp.Credentials{
			CredMode:      "auto",
			DeviceID:      "dev-1",
			AccessToken:   "tok-1",
			AccountType:   "free",
			SecretKey:     "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
			PeerPublicKey: warp.DefaultPeerPublicKey,
			AddrV4:        "172.16.0.2/32",
			Reserved:      []int{1, 2, 3},
			Endpoint:      warp.DefaultEndpoint,
			MTU:           1280,
			Keepalive:     25,
		}, nil
	}

	// Add geo file.
	addBody, _ := json.Marshal(map[string]any{"url": srv.URL + "/ai.dat", "name": "ai.dat", "autoUpdate": true})
	if w := c.do("POST", "/api/geo/files", string(addBody), true); w.Code != 200 {
		t.Fatalf("add geo file: %d %s", w.Code, w.Body.String())
	}

	// Search geo categories.
	if w := c.do("GET", "/api/geo/search?q=openai", "", false); w.Code != 200 {
		t.Fatalf("search geo: %d %s", w.Code, w.Body.String())
	}

	// Enable WARP on node.
	warpBody, _ := json.Marshal(map[string]any{
		"enabled":  true,
		"ruleMode": "rules",
		"rules":    []string{"ext-domain:ai.dat:openai"},
	})
	if w := c.do("PUT", fmt.Sprintf("/api/nodes/%d/warp", n.ID), string(warpBody), true); w.Code != 200 {
		t.Fatalf("save warp: %d %s", w.Code, w.Body.String())
	}

	// GET /api/nodes includes compact warp summary.
	w := c.do("GET", "/api/nodes", "", false)
	if w.Code != 200 {
		t.Fatalf("nodes: %d %s", w.Code, w.Body.String())
	}
	var nodes []Node
	if err := json.Unmarshal(w.Body.Bytes(), &nodes); err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].Warp == nil || !nodes[0].Warp.Enabled || nodes[0].Warp.RulesCount != 1 {
		t.Fatalf("expected warp summary on node, got %+v", nodes)
	}
}
