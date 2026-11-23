package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"

	"github.com/vyto4ka/vynel/internal/geostore"
	"github.com/vyto4ka/vynel/internal/panel/store"
	"github.com/vyto4ka/vynel/internal/warp"
	"github.com/vyto4ka/vynel/internal/xrayconf"
)

func buildMinimalGeoSite(entries map[string][]string) []byte {
	var list []byte
	for code, domains := range entries {
		var gs []byte
		gs = protowire.AppendTag(gs, 1, protowire.BytesType)
		gs = protowire.AppendString(gs, code)
		for _, dom := range domains {
			var d []byte
			d = protowire.AppendTag(d, 1, protowire.VarintType)
			d = protowire.AppendVarint(d, 2) // RootDomain
			d = protowire.AppendTag(d, 2, protowire.BytesType)
			d = protowire.AppendString(d, dom)
			gs = protowire.AppendTag(gs, 2, protowire.BytesType)
			gs = protowire.AppendBytes(gs, d)
		}
		list = protowire.AppendTag(list, 1, protowire.BytesType)
		list = protowire.AppendBytes(list, gs)
	}
	return list
}

func TestWarpAndGeoEndToEnd(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	svc := New(st)
	if err := svc.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	node, _, err := svc.CreateNode(ctx, ActorCLI, NodeInput{Code: "nl", Name: "NL", Country: "NL"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetNodeFacts(ctx, st.DB, node.ID, store.NodeFacts{Capabilities: []string{CapGeo, CapWarp}}); err != nil {
		t.Fatal(err)
	}

	// Serve a custom geosite.dat over local httptest.
	datV1 := buildMinimalGeoSite(map[string][]string{"OPENAI": {"openai.com", "chatgpt.com"}})
	datV2Missing := buildMinimalGeoSite(map[string][]string{"OTHER": {"example.org"}})
	serveV2 := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if serveV2 {
			_, _ = w.Write(datV2Missing)
			return
		}
		_, _ = w.Write(datV1)
	}))
	defer srv.Close()

	geoDir := filepath.Join(t.TempDir(), "geo")
	svc.Geo = geostore.New(geoDir, "", srv.Client())
	svc.WarpProvision = func(_ context.Context, _ int64, _ string, _ *warp.Credentials) (*warp.Credentials, error) {
		return &warp.Credentials{
			CredMode:      "auto",
			DeviceID:      "dev-nl",
			AccessToken:   "tok-nl",
			AccountType:   "free",
			SecretKey:     "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
			PublicKey:     "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=",
			PeerPublicKey: warp.DefaultPeerPublicKey,
			AddrV4:        "172.16.0.2/32",
			AddrV6:        "2606:4700:110::1/128",
			Reserved:      []int{1, 2, 3},
			Endpoint:      warp.DefaultEndpoint,
			MTU:           1280,
			Keepalive:     25,
		}, nil
	}

	// Add custom .dat file.
	gf, err := svc.AddGeoFile(ctx, ActorCLI, GeoFileAddInput{
		URL:        srv.URL + "/ai.dat",
		Name:       "ai.dat",
		Title:      "AI Rules",
		AutoUpdate: true,
	})
	if err != nil {
		t.Fatalf("AddGeoFile: %v", err)
	}
	if gf.Kind != "geosite" || gf.CategoriesCount != 1 {
		t.Fatalf("unexpected geofile: %+v", gf)
	}

	// Search by content and inspect category.
	hits, err := svc.SearchGeo(ctx, "chatgpt.com", "", "", "content", 10)
	if err != nil || len(hits) != 1 || hits[0].Rule != "ext-domain:ai.dat:openai" {
		t.Fatalf("unexpected SearchGeo content hits: %+v %v", hits, err)
	}
	det, err := svc.InspectGeoCategory(ctx, "ai.dat", "openai", "", "", 0, 50)
	if err != nil || det.Total != 2 {
		t.Fatalf("unexpected InspectGeoCategory: %+v %v", det, err)
	}

	// Enable WARP on node using the custom .dat rule (auto-provisions credentials).
	view, err := svc.SaveNodeWarp(ctx, ActorCLI, node.ID, NodeWarpInput{
		Enabled:  true,
		RuleMode: warp.RuleModeSelected,
		Rules:    []string{"ext-domain:ai.dat:openai", "104.28.0.0/16"},
	})
	if err != nil {
		t.Fatalf("SaveNodeWarp: %v", err)
	}
	if !view.Enabled || !view.HasCredentials || view.AccountType != "free" {
		t.Fatalf("unexpected warp view: %+v", view)
	}

	// DesiredState must include VYNEL_WARP outbound, rules, and ai.dat asset.
	ds, err := svc.DesiredState(ctx, node.ID)
	if err != nil {
		t.Fatalf("DesiredState: %v", err)
	}
	if len(ds.GeoAssets) != 1 || ds.GeoAssets[0].Name != "ai.dat" {
		t.Fatalf("expected ai.dat in GeoAssets, got %+v", ds.GeoAssets)
	}
	if !strings.Contains(string(ds.Config), `"tag":"`+xrayconf.WarpTag+`"`) || !strings.Contains(string(ds.Config), `"noKernelTun":true`) {
		t.Fatalf("missing VYNEL_WARP in config: %s", ds.Config)
	}

	// Preview must mask secretKey and report outbound_warp / rule_warp origins.
	preview, _, origins, err := svc.NodeConfigPreview(ctx, node.ID, "")
	if err != nil {
		t.Fatalf("NodeConfigPreview: %v", err)
	}
	rawPreview, _ := json.Marshal(preview)
	if strings.Contains(string(rawPreview), "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=") {
		t.Fatal("secretKey leaked in NodeConfigPreview")
	}
	hasOut, hasRule := false, false
	for _, o := range origins {
		if o.Kind == "outbound_warp" {
			hasOut = true
		}
		if o.Kind == "rule_warp" {
			hasRule = true
		}
	}
	if !hasOut || !hasRule {
		t.Fatalf("missing warp origins: %+v", origins)
	}

	// Deleting a geo file referenced by WARP must be rejected.
	if err := svc.DeleteGeoFile(ctx, ActorCLI, gf.ID); err == nil {
		t.Fatal("expected DeleteGeoFile to fail while referenced by node WARP")
	}

	// Refreshing with a file that dropped the used category must be rejected.
	serveV2 = true
	if _, err := svc.RefreshGeoFile(ctx, ActorCLI, gf.ID); err == nil {
		t.Fatal("expected RefreshGeoFile to fail when active category is missing")
	}

	// Fail-safe: if ai.dat disappears from disk (e.g. before restore finishes), DesiredState
	// reports a problem and skips WARP without failing the node config.
	_ = os.Remove(filepath.Join(geoDir, "ai.dat"))
	ds2, err := svc.DesiredState(ctx, node.ID)
	if err != nil {
		t.Fatalf("DesiredState missing asset: %v", err)
	}
	if len(ds2.Problems) == 0 {
		t.Fatal("expected problem when ai.dat is missing")
	}
	if strings.Contains(string(ds2.Config), xrayconf.WarpTag) {
		t.Fatal("expected WARP to be skipped when asset is missing")
	}

	// Client routing must reject server-only ext-domain: with friendly error.
	cr := DefaultClientRouting()
	cr.Proxy = []string{"ext-domain:ai.dat:openai"}
	if err := CheckClientRouting(&cr); err == nil || !strings.Contains(err.Error(), "WARP") {
		t.Fatalf("expected friendly error for ext-domain in client routing, got %v", err)
	}
}
