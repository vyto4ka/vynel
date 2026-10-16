package e2e_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/app"
	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
	"github.com/vyto4ka/vynel/internal/xray/xraytest"
)

// A cascade NL → DE on real Xray: the user's cascade server is NL's address with the cascade
// secret, and the traffic leaves through DE (it stops when DE is down, while NL itself works).
func TestCascadeEndToEnd(t *testing.T) {
	bin := xraytest.Binary(t)
	ctx := context.Background()
	const wait = 20 * time.Second

	panel := startPanel(t, t.TempDir(), "127.0.0.1:"+strconv.Itoa(xraytest.FreePort(t)))
	svc := panel.Service
	must(t, store.UpsertBaseConfig(ctx, svc.Store().DB, &store.BaseConfig{Name: "default", JSON: testBase, IsDefault: true}))
	must(t, svc.SetSetting(ctx, actor, service.SettingCaddyEnabled, "false"))
	target := xraytest.TLSTarget(t, domain)
	origin := xraytest.Origin(t)
	prof, err := svc.CreateProfile(ctx, actor, service.ProfileInput{Name: "Reality", TemplateID: "vless-reality-selfsteal",
		Values: map[string]any{"SELFSTEAL_PORT": target}})
	must(t, err)
	main, err := svc.GroupByName(ctx, "Основная")
	must(t, err)
	must(t, svc.GrantAccess(ctx, actor, main.ID, store.AccessProfile, prof.ID))

	type node struct {
		n   *store.Node
		in  *store.NodeInbound
		api int
	}
	mk := func(name, country string) node {
		n, _, err := svc.CreateNode(ctx, actor, service.NodeInput{Name: name, Country: country, Domain: domain, Local: true})
		must(t, err)
		api := xraytest.FreePort(t)
		must(t, svc.SetSetting(ctx, actor, service.SettingXrayAPIPort+"."+n.Code, strconv.Itoa(api)))
		lo, err := svc.AddAddress(ctx, actor, n.ID, "127.0.0.1", "loopback")
		must(t, err)
		in, err := svc.AttachProfile(ctx, actor, service.AttachInput{NodeID: n.ID, ProfileID: prof.ID, ListenAddressID: &lo.ID, PortOverride: xraytest.FreePort(t)})
		must(t, err)
		// The test domain does not resolve: point the connection point at the loopback.
		must(t, svc.SetHostOverride(ctx, actor, in.ID, map[string]any{"address": "127.0.0.1"}))
		return node{n, in, api}
	}
	nl, de := mk("Нидерланды", "nl"), mk("Германия", "de")
	c, err := svc.CreateCascade(ctx, actor, service.CascadeInput{Name: "NL-DE", EntryInboundID: nl.in.ID, ExitInboundID: de.in.ID, Enabled: true})
	must(t, err)
	alice, err := svc.CreateUser(ctx, actor, service.CreateUserInput{Username: "alice"})
	must(t, err)
	_, err = svc.PutGrant(ctx, actor, service.GrantInput{UserID: alice.ID, Kind: store.GrantCascade, RefID: c.ID})
	must(t, err)

	startAgent(t, bin, filepath.Join(t.TempDir(), "nl"), app.LocalDialer(ctx, panel.Reconciler, nl.n.ID), nl.api)
	nodeDE := startAgent(t, bin, filepath.Join(t.TempDir(), "de"), app.LocalDialer(ctx, panel.Reconciler, de.n.ID), de.api)
	xraytest.Eventually(t, wait, "nodes converge", inSync(ctx, svc, nl.n.Code, de.n.Code))

	r, err := svc.RenderNodeInbound(ctx, nl.in.ID)
	must(t, err)
	client := func(uuid string) int {
		return xraytest.StartRealityClient(t, bin, xraytest.RealityClient{
			ServerPort: nl.in.PortOverride, UUID: uuid, Flow: r.Flow, ServerName: domain,
			PublicKey: r.Values["REALITY_PUBLIC_KEY"].(string), ShortID: r.Values["REALITY_SHORT_ID"].(string),
		})
	}
	direct, cascade := client(alice.UUID), client(service.CascadeUUID(alice.UUID, c.ID))
	fetch := func(socks int) func() error {
		return func() error {
			b, err := xraytest.Fetch(ctx, socks, origin)
			if err == nil && len(b) != xraytest.OriginSize {
				err = fmt.Errorf("short body %d", len(b))
			}
			return err
		}
	}
	xraytest.Eventually(t, wait, "alice through NL", fetch(direct))
	xraytest.Eventually(t, wait, "alice through the cascade", fetch(cascade))
	// The cascade traffic counts for alice.
	xraytest.Eventually(t, wait, "cascade traffic counted", func() error {
		byNode, err := svc.UserTrafficByNode(ctx, alice.ID, 1)
		if err != nil {
			return err
		}
		if len(byNode) == 0 {
			return fmt.Errorf("no traffic yet")
		}
		return nil
	})

	nodeDE.stop()
	if err := fetch(cascade)(); err == nil {
		t.Fatal("the cascade works without its exit node")
	}
	if err := fetch(direct)(); err != nil {
		t.Fatalf("NL itself stopped working: %v", err)
	}
}
