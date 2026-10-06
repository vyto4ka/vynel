// Package e2e runs the panel, two node agents (one over gRPC/mTLS, one in-process) and real Xray
// processes on localhost — the stage 3 acceptance test from docs/ROADMAP.md.
package e2e_test

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vyto4ka/vynel/internal/jointoken"
	"github.com/vyto4ka/vynel/internal/node/agent"
	"github.com/vyto4ka/vynel/internal/panel/app"
	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
	nodev1 "github.com/vyto4ka/vynel/internal/proto/vynel/node/v1"
	"github.com/vyto4ka/vynel/internal/xray"
	"github.com/vyto4ka/vynel/internal/xray/xraytest"
)

// testBase lets traffic reach 127.0.0.1 (the default base blocks geoip:private).
const testBase = `{"log":{"loglevel":"warning"},"outbounds":[{"tag":"DIRECT","protocol":"freedom"}]}`

const domain = "example.test"

var actor = service.Actor{Kind: "test"}

type panelRun struct {
	*app.Panel
	stop    func()
	subAddr string
}

func startPanel(t *testing.T, dir, gwAddr string) *panelRun {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	p, closeFn, err := app.Open(ctx, app.Config{DataDir: dir, GatewayAddr: gwAddr, Log: logger("panel")})
	if err != nil {
		t.Fatal(err)
	}
	p.Reconciler.Debounce, p.Reconciler.PollInterval = 50*time.Millisecond, 200*time.Millisecond
	done := make(chan struct{}, 3)
	subAddr := "127.0.0.1:" + strconv.Itoa(xraytest.FreePort(t))
	if err := p.Service.SetSetting(ctx, actor, service.SettingSubListen, subAddr); err != nil {
		t.Fatal(err)
	}
	go func() { _ = p.Reconciler.Run(ctx); done <- struct{}{} }()
	go func() { _ = p.Gateway.Serve(ctx, gwAddr); done <- struct{}{} }()
	go func() { _ = p.Subscriptions.Serve(ctx, subAddr); done <- struct{}{} }()
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		<-done
		<-done
		<-done
		closeFn()
	}
	t.Cleanup(stop)
	return &panelRun{Panel: p, stop: stop, subAddr: subAddr}
}

func logger(component string) *slog.Logger {
	level := slog.LevelWarn
	if os.Getenv("E2E_DEBUG") != "" {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})).With("c", component)
}

type nodeRun struct {
	agent *agent.Agent
	stop  func()
	api   *xray.API
}

func startAgent(t *testing.T, bin xray.Binary, dir string, dial agent.Dialer, apiPort int) *nodeRun {
	t.Helper()
	a, err := agent.New(agent.Config{
		DataDir: dir, Xray: bin, Version: "test", Dial: dial, Log: logger("agent-" + filepath.Base(dir)),
		Addresses: func() []*nodev1.Address { return nil }, MaxBackoff: 500 * time.Millisecond, StatsInterval: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = a.Run(ctx); close(done) }()
	api, err := xray.DialAPI("127.0.0.1:" + strconv.Itoa(apiPort))
	if err != nil {
		t.Fatal(err)
	}
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		<-done
		a.Close()
		api.Close()
	}
	t.Cleanup(stop)
	return &nodeRun{agent: a, stop: stop, api: api}
}

// users returns the emails configured on an inbound of a running Xray.
func users(ctx context.Context, n *nodeRun, tag string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	us, err := n.api.InboundUsers(ctx, tag)
	slices.Sort(us)
	return us, err
}

func wantUsers(ctx context.Context, n *nodeRun, tag string, want ...string) func() error {
	slices.Sort(want)
	return func() error {
		got, err := users(ctx, n, tag)
		if err != nil {
			return err
		}
		if !slices.Equal(got, want) {
			return fmt.Errorf("users on %s: got %v want %v", tag, got, want)
		}
		return nil
	}
}

func inSync(ctx context.Context, svc *service.Service, codes ...string) func() error {
	return func() error {
		for _, code := range codes {
			n, err := svc.NodeByCode(ctx, code)
			if err != nil {
				return err
			}
			if n.AppliedHash == "" || n.AppliedHash != n.DesiredHash {
				return fmt.Errorf("node %s not in sync (applied %.8s desired %.8s, err %q)", code, n.AppliedHash, n.DesiredHash, n.LastError)
			}
		}
		return nil
	}
}

func TestPanelAndNodesEndToEnd(t *testing.T) {
	bin := xraytest.Binary(t)
	ctx := context.Background()
	const wait = 20 * time.Second

	dir := t.TempDir()
	gwAddr := "127.0.0.1:" + strconv.Itoa(xraytest.FreePort(t))
	panel := startPanel(t, dir, gwAddr)
	svc := panel.Service
	db := svc.Store().DB
	if err := store.UpsertBaseConfig(ctx, db, &store.BaseConfig{Name: "default", JSON: testBase, IsDefault: true}); err != nil {
		t.Fatal(err)
	}

	target := xraytest.TLSTarget(t, domain)
	origin := xraytest.Origin(t)

	// Two nodes with the same profile, each with its own inbound, keys, port and Xray API port.
	nl, nlSecret, err := svc.CreateNode(ctx, actor, service.NodeInput{Name: "Нидерланды", Country: "nl", Domain: domain})
	if err != nil {
		t.Fatal(err)
	}
	de, _, err := svc.CreateNode(ctx, actor, service.NodeInput{Name: "Германия", Country: "de", Domain: domain, Local: true})
	if err != nil {
		t.Fatal(err)
	}
	apiNL, apiDE := xraytest.FreePort(t), xraytest.FreePort(t)
	must(t, svc.SetSetting(ctx, actor, service.SettingXrayAPIPort+".NL", strconv.Itoa(apiNL)))
	must(t, svc.SetSetting(ctx, actor, service.SettingXrayAPIPort+".DE", strconv.Itoa(apiDE)))
	// Reality targets a test TLS server here; Caddy is covered by TestAllInOneOneIPOneDomain.
	must(t, svc.SetSetting(ctx, actor, service.SettingCaddyEnabled, "false"))

	prof, err := svc.CreateProfile(ctx, actor, service.ProfileInput{Name: "Reality", TemplateID: "vless-reality-selfsteal",
		Values: map[string]any{"SELFSTEAL_PORT": target}})
	if err != nil {
		t.Fatal(err)
	}
	main, err := svc.GroupByName(ctx, "Основная")
	if err != nil {
		t.Fatal(err)
	}
	must(t, svc.GrantAccess(ctx, actor, main.ID, store.AccessProfile, prof.ID))
	attach := func(n *store.Node) *store.NodeInbound {
		lo, err := svc.AddAddress(ctx, actor, n.ID, "127.0.0.1", "loopback")
		if err != nil {
			t.Fatal(err)
		}
		ni, err := svc.AttachProfile(ctx, actor, service.AttachInput{NodeID: n.ID, ProfileID: prof.ID, ListenAddressID: &lo.ID, PortOverride: xraytest.FreePort(t)})
		if err != nil {
			t.Fatal(err)
		}
		return ni
	}
	nlIn, deIn := attach(nl), attach(de)

	// NL joins over gRPC with the join token; DE runs in-process like `panel --with-node`.
	token := jointoken.Token{Addr: gwAddr, SNI: panel.SNI, CAFingerprint: panel.CA.Fingerprint(), Secret: nlSecret}.Encode()
	nlDir := filepath.Join(t.TempDir(), "nl")
	if _, err := agent.Join(ctx, nlDir, token); err != nil {
		t.Fatal(err)
	}
	if _, err := agent.Join(ctx, t.TempDir(), token); err == nil {
		t.Fatal("a join token must be single-use")
	}
	nlDial, closeDial, err := agent.GRPCDialer(nlDir)
	if err != nil {
		t.Fatal(err)
	}
	defer closeDial()
	nodeNL := startAgent(t, bin, nlDir, nlDial, apiNL)
	deDir := filepath.Join(t.TempDir(), "de")
	nodeDE := startAgent(t, bin, deDir, app.LocalDialer(ctx, panel.Reconciler, de.ID), apiDE)

	xraytest.Eventually(t, wait, "nodes converge", inSync(ctx, svc, "NL", "DE"))

	// A new user appears on both nodes without restarts.
	alice, err := svc.CreateUser(ctx, actor, service.CreateUserInput{Username: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	ea := service.UserEmail(alice.ID)
	xraytest.Eventually(t, wait, "alice on NL", wantUsers(ctx, nodeNL, nlIn.Tag, ea))
	xraytest.Eventually(t, wait, "alice on DE", wantUsers(ctx, nodeDE, deIn.Tag, ea))

	r, err := svc.RenderNodeInbound(ctx, nlIn.ID)
	if err != nil {
		t.Fatal(err)
	}
	socks := xraytest.StartRealityClient(t, bin, xraytest.RealityClient{
		ServerPort: nlIn.PortOverride, UUID: alice.UUID, Flow: r.Flow, ServerName: domain,
		PublicKey: r.Values["REALITY_PUBLIC_KEY"].(string), ShortID: r.Values["REALITY_SHORT_ID"].(string),
	})
	fetchOK := func() error {
		b, err := xraytest.Fetch(ctx, socks, origin)
		if err != nil {
			return err
		}
		if len(b) != xraytest.OriginSize {
			return fmt.Errorf("short body %d", len(b))
		}
		return nil
	}
	xraytest.Eventually(t, wait, "alice connects through NL", fetchOK)

	// The subscription (Happ user agent, with HWID) gives a link that works as is.
	nlLink := fetchSubscriptionLink(t, panel, alice, nlIn.Tag, "dev-1")
	u, err := url.Parse(nlLink)
	if err != nil || u.Scheme != "vless" {
		t.Fatalf("bad link %q: %v", nlLink, err)
	}
	q := u.Query()
	linkPort, _ := strconv.Atoi(u.Port())
	linkSocks := xraytest.StartRealityClient(t, bin, xraytest.RealityClient{
		ServerPort: linkPort, UUID: u.User.Username(), Flow: q.Get("flow"), ServerName: q.Get("sni"),
		PublicKey: q.Get("pbk"), ShortID: q.Get("sid"),
	})
	if b, err := xraytest.Fetch(ctx, linkSocks, origin); err != nil || len(b) != xraytest.OriginSize {
		t.Fatalf("client built from the subscription failed: %v (link %s)", err, nlLink)
	}
	if u.Fragment != "🇳🇱 Нидерланды" {
		t.Fatalf("remark %q", u.Fragment)
	}

	// Disabling removes her everywhere; enabling brings her back.
	if _, err := svc.SetUserEnabled(ctx, actor, alice.ID, false); err != nil {
		t.Fatal(err)
	}
	xraytest.Eventually(t, wait, "alice removed from NL", wantUsers(ctx, nodeNL, nlIn.Tag))
	xraytest.Eventually(t, wait, "alice removed from DE", wantUsers(ctx, nodeDE, deIn.Tag))
	if err := fetchOK(); err == nil {
		t.Fatal("disabled user still connects")
	}
	if _, err := svc.SetUserEnabled(ctx, actor, alice.ID, true); err != nil {
		t.Fatal(err)
	}
	xraytest.Eventually(t, wait, "alice back on NL", wantUsers(ctx, nodeNL, nlIn.Tag, ea))

	// A structural profile change restarts Xray via a snapshot; users survive the restart.
	if _, err := svc.UpdateProfile(ctx, actor, prof.ID, service.ProfileInput{Override: map[string]any{"sniffing": map[string]any{"enabled": false}}}); err != nil {
		t.Fatal(err)
	}
	xraytest.Eventually(t, wait, "converge after profile change", inSync(ctx, svc, "NL", "DE"))
	xraytest.Eventually(t, wait, "alice kept after restart", wantUsers(ctx, nodeNL, nlIn.Tag, ea))
	xraytest.Eventually(t, wait, "traffic after restart", fetchOK)

	// Panel outage: the node keeps serving, even across an agent restart (saved state).
	panel.stop()
	if err := fetchOK(); err != nil {
		t.Fatalf("node must keep working without the panel: %v", err)
	}
	nodeNL.stop()
	nodeNL = startAgent(t, bin, nlDir, nlDial, apiNL)
	xraytest.Eventually(t, wait, "node restarts from saved state without the panel", fetchOK)

	// Panel back: changes made meanwhile (here: right after restart) reach the nodes.
	panel = startPanel(t, dir, gwAddr)
	svc = panel.Service
	bob, err := svc.CreateUser(ctx, actor, service.CreateUserInput{Username: "bob"})
	if err != nil {
		t.Fatal(err)
	}
	eb := service.UserEmail(bob.ID)
	xraytest.Eventually(t, wait, "bob reaches NL after panel restart", wantUsers(ctx, nodeNL, nlIn.Tag, ea, eb))
	// The in-process node is bound to the panel process, so it restarts with it.
	nodeDE.stop()
	nodeDE = startAgent(t, bin, deDir, app.LocalDialer(ctx, panel.Reconciler, de.ID), apiDE)
	xraytest.Eventually(t, wait, "bob reaches DE", wantUsers(ctx, nodeDE, deIn.Tag, ea, eb))

	// Changes made by another process (the CLI) are picked up through the outbox poll.
	cli, err := store.Open(ctx, filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	carol, err := service.New(cli).CreateUser(ctx, service.ActorCLI, service.CreateUserInput{Username: "carol"})
	cli.Close()
	if err != nil {
		t.Fatal(err)
	}
	xraytest.Eventually(t, wait, "carol from another process", wantUsers(ctx, nodeNL, nlIn.Tag, ea, eb, service.UserEmail(carol.ID)))

	// Traffic reaches the panel; crossing the limit removes the user from the nodes.
	xraytest.Eventually(t, wait, "alice's traffic is counted", func() error {
		u, err := svc.User(ctx, alice.ID)
		if err != nil {
			return err
		}
		if u.TrafficUsedBytes < xraytest.OriginSize {
			return fmt.Errorf("used %d", u.TrafficUsedBytes)
		}
		return nil
	})
	cur, err := svc.User(ctx, alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	limit := cur.TrafficUsedBytes + 1000
	if _, err := svc.SetUserTrafficLimit(ctx, actor, alice.ID, &limit); err != nil {
		t.Fatal(err)
	}
	if err := fetchOK(); err != nil {
		t.Fatalf("still under the limit: %v", err)
	}
	xraytest.Eventually(t, wait, "alice becomes limited", func() error {
		u, err := svc.User(ctx, alice.ID)
		if err != nil {
			return err
		}
		if u.Status != store.StatusLimited {
			return fmt.Errorf("status %s used %d limit %d", u.Status, u.TrafficUsedBytes, limit)
		}
		return nil
	})
	xraytest.Eventually(t, wait, "limited alice removed from NL", wantUsers(ctx, nodeNL, nlIn.Tag, eb, service.UserEmail(carol.ID)))
	ov, err := svc.Overview(ctx)
	if err != nil || ov.TodayBytes < 2*xraytest.OriginSize {
		t.Fatalf("overview %+v %v", ov, err)
	}

	// Revoking the node certificate drops the session and blocks reconnects.
	if _, err := svc.ReissueInstallToken(ctx, actor, nl.ID); err != nil {
		t.Fatal(err)
	}
	xraytest.Eventually(t, wait, "revoked node disconnected", func() error {
		if panel.Reconciler.Connected(nl.ID) {
			return errors.New("still connected")
		}
		return nil
	})
	time.Sleep(2 * time.Second)
	if panel.Reconciler.Connected(nl.ID) {
		t.Fatal("revoked node reconnected")
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// fetchSubscriptionLink downloads the user's subscription as Happ would and returns the link of tag.
func fetchSubscriptionLink(t *testing.T, panel *panelRun, u *store.User, tag, hwid string) string {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, "http://"+panel.subAddr+service.DefaultSubPrefix+u.SubToken, nil)
	req.Header.Set("User-Agent", "Happ/3.0")
	req.Header.Set("x-hwid", hwid)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Subscription-Userinfo"), "expire=") {
		t.Fatalf("subscription status %d headers %v", resp.StatusCode, resp.Header)
	}
	raw, err := base64.StdEncoding.DecodeString(string(body))
	if err != nil {
		t.Fatalf("not base64: %q", body)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.Contains(line, "pbk=") && strings.Contains(line, "@"+domain+":") {
			l, _ := url.Parse(line)
			p, _ := strconv.Atoi(l.Port())
			ni, err := panel.Service.NodeInboundByTag(context.Background(), tag)
			if err == nil && p == ni.PortOverride {
				return line
			}
		}
	}
	t.Fatalf("no link for %s in %s", tag, raw)
	return ""
}

func decodeB64(s string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	return string(b), err
}
