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
	"sync/atomic"
	"testing"
	"time"

	"github.com/vyto4ka/vynel/internal/node/agent"
	"github.com/vyto4ka/vynel/internal/panel/app"
	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
	nodev1 "github.com/vyto4ka/vynel/internal/proto/vynel/node/v1"
	"github.com/vyto4ka/vynel/internal/update"
	"github.com/vyto4ka/vynel/internal/xray/xraytest"
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

	restarted := make(chan string, 2)
	var updState atomic.Pointer[update.Status]
	a, err := agent.New(agent.Config{
		DataDir: filepath.Join(t.TempDir(), "node"), Xray: bin, Version: "test", CaddyBin: caddyBin, NginxUser: nginxUser(),
		Dial: app.LocalDialer(ctx, panel.Reconciler, node.ID), Log: logger("aio-node"),
		Addresses: func() []*nodev1.Address { return nil }, MaxBackoff: 500 * time.Millisecond, StatsInterval: 200 * time.Millisecond,
		Restart: func() { restarted <- "service" }, Reboot: func() error { restarted <- "reboot"; return nil },
		// A fake update: it says it runs, logs two steps and finishes, as update.Run would.
		Update: func(context.Context) error {
			go func() {
				now := time.Now().Unix()
				updState.Store(&update.Status{Running: true, StartedAt: now, Log: "vynel: downloading"})
				time.Sleep(300 * time.Millisecond)
				updState.Store(&update.Status{Running: true, StartedAt: now, Log: "vynel: downloading\nrestarting vynel"})
				time.Sleep(300 * time.Millisecond)
				updState.Store(&update.Status{OK: true, StartedAt: now, FinishedAt: time.Now().Unix(), Version: "vynel new", Log: "update finished"})
			}()
			return nil
		},
		UpdateStatus: func() *update.Status { return updState.Load() },
		UpdateEvery:  100 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	actx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { _ = a.Run(actx); close(done) }()
	t.Cleanup(func() { cancel(); <-done; a.Close() })
	xraytest.Eventually(t, wait, "node converges (xray + caddy)", inSync(ctx, svc, node.Code))

	// A browser on the public port sees an ordinary website.
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
		if resp.StatusCode != 200 || !strings.Contains(page, `class="brand"`) {
			return fmt.Errorf("status %d body %.80q", resp.StatusCode, page)
		}
		// A plain "nginx", like most of the web; never a version.
		if s := resp.Header.Get("Server"); s != "" && s != "nginx" {
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

	// The web panel answers under its secret path on the same domain; the login works through Caddy.
	pw, err := svc.SetAdminCredentials(ctx, actor, "admin")
	if err != nil {
		t.Fatal(err)
	}
	webURL, err := svc.WebURL(ctx)
	if err != nil || !strings.HasPrefix(webURL, base+"/") {
		t.Fatalf("web url %q %v", webURL, err)
	}
	login, _ := http.NewRequest(http.MethodPost, webURL+"api/login", strings.NewReader(`{"login":"admin","password":"`+pw+`"}`))
	login.Header.Set("X-Vynel", "1")
	resp, err = web.Do(login)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	var session *http.Cookie
	for _, c := range resp.Cookies() {
		session = c
	}
	if resp.StatusCode != 200 || session == nil || !session.Secure {
		t.Fatalf("web login through caddy: %d cookie %+v", resp.StatusCode, session)
	}
	me, _ := http.NewRequest(http.MethodGet, webURL+"api/users", nil)
	me.AddCookie(session)
	resp, err = web.Do(me)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), `"alice"`) {
		t.Fatalf("web api through caddy: %d %s", resp.StatusCode, b)
	}

	// A random path per session: the login link opens its own path on the same domain. The
	// self-steal decoy's catch-all used to come first, so the panel had no route at all.
	must(t, svc.SetLoginMode(ctx, actor, service.ModeLink))
	must(t, svc.SetSessionPaths(ctx, actor, true))
	xraytest.Eventually(t, wait, "node takes the panel's whole-domain route", inSync(ctx, svc, node.Code))
	link, _, err := svc.LoginLink(ctx, actor)
	if err != nil {
		t.Fatal(err)
	}
	lu, _ := url.Parse(link)
	own := base + lu.Path
	if own == webURL {
		t.Fatalf("the link opens the fixed path: %s", link)
	}
	for _, page := range []string{own, webURL} { // its own path; the fixed one (the Mini App)
		xraytest.Eventually(t, wait, "panel at "+page, func() error {
			resp, err := web.Get(page + "api/login/options")
			if err != nil {
				return err
			}
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != 200 || !strings.Contains(string(b), `"mode":"link"`) {
				return fmt.Errorf("status %d body %.80q", resp.StatusCode, b)
			}
			return nil
		})
	}
	magic, _ := http.NewRequest(http.MethodPost, own+"api/login/magic", strings.NewReader(`{"token":"`+strings.TrimPrefix(lu.Fragment, "/magic/")+`"}`))
	magic.Header.Set("X-Vynel", "1")
	resp, err = web.Do(magic)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	session = nil
	for _, c := range resp.Cookies() {
		session = c
	}
	if resp.StatusCode != 200 || session == nil || session.Path != lu.Path {
		t.Fatalf("link login on its own path: %d cookie %+v", resp.StatusCode, session)
	}
	me, _ = http.NewRequest(http.MethodGet, own+"api/users", nil)
	me.AddCookie(session)
	resp, err = web.Do(me)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), `"alice"`) {
		t.Fatalf("web api on the session path: %d %s", resp.StatusCode, b)
	}
	resp, err = web.Get(base + "/AbCdEfGhJkMnPqRs/")
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 404 || !strings.Contains(string(b), `class="brand"`) {
		t.Fatalf("an unknown path must be the decoy: %d %.80q", resp.StatusCode, b)
	}

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

	// Admin actions: Xray and Caddy restart in place and the VPN comes back; a service restart
	// and a reboot are answered first and then handed to the hooks.
	for _, act := range []nodev1.Command_Action{nodev1.Command_ACTION_RESTART_XRAY, nodev1.Command_ACTION_RESTART_CADDY} {
		cctx, ccancel := context.WithTimeout(ctx, wait)
		res, err := panel.Reconciler.Command(cctx, node.ID, act)
		ccancel()
		if err != nil || res.Error != "" {
			t.Fatalf("%v: %v %+v", act, err, res)
		}
		xraytest.Eventually(t, wait, "VPN after "+act.String(), func() error {
			b, err := xraytest.Fetch(ctx, socks, origin)
			if err == nil && len(b) != xraytest.OriginSize {
				err = fmt.Errorf("short body %d", len(b))
			}
			return err
		})
	}
	for act, want := range map[nodev1.Command_Action]string{nodev1.Command_ACTION_RESTART_AGENT: "service", nodev1.Command_ACTION_REBOOT: "reboot"} {
		cctx, ccancel := context.WithTimeout(ctx, wait)
		res, err := panel.Reconciler.Command(cctx, node.ID, act)
		ccancel()
		if err != nil || res.Error != "" || res.Detail == "" {
			t.Fatalf("%v: %v %+v", act, err, res)
		}
		select {
		case got := <-restarted:
			if got != want {
				t.Fatalf("%v ran %q", act, got)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%v: hook not called", act)
		}
	}
	// An update: the command is accepted, the node reports it running with its log, then done.
	cctx, ccancel := context.WithTimeout(ctx, wait)
	res, err := panel.Reconciler.Command(cctx, node.ID, nodev1.Command_ACTION_UPDATE)
	ccancel()
	if err != nil || res.Error != "" {
		t.Fatalf("update: %v %+v", err, res)
	}
	var sawRunning bool
	xraytest.Eventually(t, wait, "update reported as done", func() error {
		st := panel.Reconciler.UpdateStatus(node.ID)
		switch {
		case st == nil:
			return fmt.Errorf("no status yet")
		case st.Running:
			sawRunning = sawRunning || strings.Contains(st.Log, "downloading")
			return fmt.Errorf("running: %q", st.Log)
		case !st.OK || st.Version != "vynel new":
			return fmt.Errorf("finished: %+v", st)
		}
		return nil
	})
	if !sawRunning {
		t.Fatal("the running update and its log never reached the panel")
	}

	if _, err := panel.Reconciler.Command(ctx, 9999, nodev1.Command_ACTION_RESTART_XRAY); err == nil {
		t.Fatal("a command to a node that is not connected must fail")
	}
}

func decodeLinks(t *testing.T, body []byte) []string {
	t.Helper()
	raw, err := decodeB64(string(body))
	if err != nil {
		t.Fatalf("subscription is not base64: %q", body)
	}
	return strings.Split(raw, "\n")
}

// nginxUser runs nginx workers as root in tests: temp dirs are not readable by www-data.
func nginxUser() string {
	if os.Getuid() == 0 {
		return "root"
	}
	return ""
}
