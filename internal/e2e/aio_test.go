package e2e_test

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vyto4ka/vynnel/internal/node/agent"
	"github.com/vyto4ka/vynnel/internal/panel/app"
	"github.com/vyto4ka/vynnel/internal/panel/service"
	"github.com/vyto4ka/vynnel/internal/panel/store"
	nodev1 "github.com/vyto4ka/vynnel/internal/proto/vynnel/node/v1"
	"github.com/vyto4ka/vynnel/internal/xray/xraytest"
)

// TestAllInOneOneIPOneDomain: panel + node on one server, one IP, one domain. Reality owns the
// public port; everything that is not a VPN client (browsers, subscription fetches) falls through
// to Caddy, which serves the decoy site and the subscription prefix on the same domain.
func TestAllInOneOneIPOneDomain(t *testing.T) {
	bin := xraytest.Binary(t)
	caddyBin := os.Getenv("CADDY_BIN")
	if caddyBin == "" {
		t.Skip("CADDY_BIN not set; run `make test-integration`")
	}
	ctx := context.Background()
	const wait = 30 * time.Second

	dir := t.TempDir()
	panel := startPanel(t, dir, "127.0.0.1:"+strconv.Itoa(xraytest.FreePort(t)))
	svc := panel.Service
	must(t, store.UpsertBaseConfig(ctx, svc.Store().DB, &store.BaseConfig{Name: "default", JSON: testBase, IsDefault: true}))

	publicPort, selfsteal := xraytest.FreePort(t), xraytest.FreePort(t)
	for k, v := range map[string]string{
		service.SettingCaddyIssuer:    "internal", // no public ACME in tests
		service.SettingCaddyHTTPPort:  strconv.Itoa(xraytest.FreePort(t)),
		service.SettingCaddyAdminPort: strconv.Itoa(xraytest.FreePort(t)),
		service.SettingXrayAPIPort:    strconv.Itoa(xraytest.FreePort(t)),
		service.SettingSubDomain:      domain, // the same domain as the node: the minimal setup
		service.SettingSubPort:        strconv.Itoa(publicPort),
	} {
		must(t, svc.SetSetting(ctx, actor, k, v))
	}

	node, err := svc.EnsureLocalNode(ctx, service.NodeInput{Name: "Нидерланды", Country: "nl", Domain: domain})
	if err != nil {
		t.Fatal(err)
	}
	prof, err := svc.CreateProfile(ctx, actor, service.ProfileInput{Name: "Reality", TemplateID: "vless-reality-selfsteal",
		Values: map[string]any{"SELFSTEAL_PORT": selfsteal}})
	if err != nil {
		t.Fatal(err)
	}
	main, _ := svc.GroupByName(ctx, "Основная")
	must(t, svc.GrantAccess(ctx, actor, main.ID, store.AccessProfile, prof.ID))
	lo, err := svc.AddAddress(ctx, actor, node.ID, "127.0.0.1", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AttachProfile(ctx, actor, service.AttachInput{NodeID: node.ID, ProfileID: prof.ID, ListenAddressID: &lo.ID, PortOverride: publicPort}); err != nil {
		t.Fatal(err)
	}
	ds, err := svc.DesiredState(ctx, node.ID)
	if err != nil || len(ds.Problems) > 0 {
		t.Fatalf("desired state: %v %v", err, ds.Problems)
	}

	a, err := agent.New(agent.Config{
		DataDir: filepath.Join(t.TempDir(), "node"), Xray: bin, Version: "test", CaddyBin: caddyBin,
		Dial: app.LocalDialer(ctx, panel.Reconciler, node.ID), Log: logger("aio-node"),
		Addresses: func() []*nodev1.Address { return nil }, MaxBackoff: 500 * time.Millisecond, StatsInterval: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	actx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { _ = a.Run(actx); close(done) }()
	t.Cleanup(func() { cancel(); <-done; a.Close() })
	xraytest.Eventually(t, wait, "node converges (xray + caddy)", inSync(ctx, svc, node.Code))

	// A browser on the public port sees an ordinary website with no server banner.
	web := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{ServerName: domain, InsecureSkipVerify: true}, // internal CA in tests
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", "127.0.0.1:"+strconv.Itoa(publicPort))
		},
	}}
	base := fmt.Sprintf("https://%s:%d", domain, publicPort)
	var page string
	xraytest.Eventually(t, wait, "decoy site through Reality", func() error {
		resp, err := web.Get(base + "/")
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		page = string(b)
		if resp.StatusCode != 200 || !strings.Contains(page, "<header><b>") {
			return fmt.Errorf("status %d body %.80q", resp.StatusCode, page)
		}
		if s := resp.Header.Get("Server"); s != "" {
			return fmt.Errorf("server banner %q", s)
		}
		return nil
	})
	resp, err := web.Get(base + "/wp-admin")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 404 || !strings.Contains(string(b), "404") {
		t.Fatalf("unknown path: %d %s", resp.StatusCode, b)
	}

	// The subscription is served on the same domain and port, behind Reality.
	alice, err := svc.CreateUser(ctx, actor, service.CreateUserInput{Username: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	subURL, err := svc.SubscriptionURL(ctx, alice)
	if err != nil || !strings.HasPrefix(subURL, base+"/s/") {
		t.Fatalf("sub url %q %v", subURL, err)
	}
	req, _ := http.NewRequest(http.MethodGet, subURL, nil)
	req.Header.Set("User-Agent", "Happ/3")
	req.Header.Set("x-hwid", "phone-1")
	resp, err = web.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	links := decodeLinks(t, body)
	if len(links) != 1 {
		t.Fatalf("links %v", links)
	}
	l, _ := url.Parse(links[0])
	q := l.Query()

	// And the VPN itself works: Reality's target is the real Caddy now.
	xraytest.Eventually(t, wait, "alice is on the node", inSync(ctx, svc, node.Code))
	socks := xraytest.StartRealityClient(t, bin, xraytest.RealityClient{
		ServerPort: publicPort, UUID: l.User.Username(), Flow: q.Get("flow"), ServerName: q.Get("sni"),
		PublicKey: q.Get("pbk"), ShortID: q.Get("sid"),
	})
	origin := xraytest.Origin(t)
	xraytest.Eventually(t, wait, "VPN through Reality with Caddy as target", func() error {
		b, err := xraytest.Fetch(ctx, socks, origin)
		if err != nil {
			return err
		}
		if len(b) != xraytest.OriginSize {
			return fmt.Errorf("short body %d", len(b))
		}
		return nil
	})
}

func decodeLinks(t *testing.T, body []byte) []string {
	t.Helper()
	raw, err := decodeB64(string(body))
	if err != nil {
		t.Fatalf("subscription is not base64: %q", body)
	}
	return strings.Split(raw, "\n")
}
