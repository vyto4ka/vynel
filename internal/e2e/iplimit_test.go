package e2e_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/proxy"

	"github.com/vyto4ka/vynel/internal/panel/app"
	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
	"github.com/vyto4ka/vynel/internal/xray/xraytest"
)

// device is an app on its own address with one connection kept open, so the node sees the
// address online between requests.
type device struct {
	hc *http.Client
}

func newDevice(socks int) *device {
	d, _ := proxy.SOCKS5("tcp", "127.0.0.1:"+strconv.Itoa(socks), nil, proxy.Direct)
	return &device{hc: &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DialContext: d.(proxy.ContextDialer).DialContext}}}
}

func (d *device) get(url string) error {
	resp, err := d.hc.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	n, err := io.Copy(io.Discard, resp.Body)
	if err == nil && n != xraytest.OriginSize {
		err = fmt.Errorf("short body %d", n)
	}
	return err
}

// fresh makes a request on a new connection.
func (d *device) fresh(url string) error {
	d.hc.CloseIdleConnections()
	return d.get(url)
}

// A user with a limit of one address: the second address gets in once, then the panel sees it
// on the node and blocks it there (and its subscription shows the limit); when the first device
// leaves, the second one takes its place.
func TestIPLimitEndToEnd(t *testing.T) {
	bin := xraytest.Binary(t)
	ctx := context.Background()
	const wait = 20 * time.Second

	panel := startPanel(t, t.TempDir(), "127.0.0.1:"+strconv.Itoa(xraytest.FreePort(t)))
	svc := panel.Service
	must(t, store.UpsertBaseConfig(ctx, svc.Store().DB, &store.BaseConfig{Name: "default", JSON: testBase, IsDefault: true}))
	tr := svc.IPs()
	tr.Confirm, tr.Grace = 500*time.Millisecond, 2*time.Second

	target := xraytest.TLSTarget(t, domain)
	origin := xraytest.Origin(t)
	de, _, err := svc.CreateNode(ctx, actor, service.NodeInput{Name: "Германия", Country: "de", Domain: domain, Local: true})
	must(t, err)
	apiDE := xraytest.FreePort(t)
	must(t, svc.SetSetting(ctx, actor, service.SettingXrayAPIPort+".DE", strconv.Itoa(apiDE)))
	must(t, svc.SetSetting(ctx, actor, service.SettingCaddyEnabled, "false"))
	must(t, svc.SetSetting(ctx, actor, service.SettingSubDomain, "sub.example.test"))
	prof, err := svc.CreateProfile(ctx, actor, service.ProfileInput{Name: "Reality", TemplateID: "vless-reality-selfsteal",
		Values: map[string]any{"SELFSTEAL_PORT": target}})
	must(t, err)
	main, err := svc.GroupByName(ctx, "Основная")
	must(t, err)
	must(t, svc.GrantAccess(ctx, actor, main.ID, store.AccessProfile, prof.ID))
	lo, err := svc.AddAddress(ctx, actor, de.ID, "127.0.0.1", "loopback")
	must(t, err)
	in, err := svc.AttachProfile(ctx, actor, service.AttachInput{NodeID: de.ID, ProfileID: prof.ID, ListenAddressID: &lo.ID, PortOverride: xraytest.FreePort(t)})
	must(t, err)
	one := int64(1)
	alice, err := svc.CreateUser(ctx, actor, service.CreateUserInput{Username: "alice", IPLimit: &one})
	must(t, err)

	startAgent(t, bin, filepath.Join(t.TempDir(), "de"), app.LocalDialer(ctx, panel.Reconciler, de.ID), apiDE)
	xraytest.Eventually(t, wait, "node converges", inSync(ctx, svc, "DE"))

	r, err := svc.RenderNodeInbound(ctx, in.ID)
	must(t, err)
	client := func(from string) *device {
		return newDevice(xraytest.StartRealityClient(t, bin, xraytest.RealityClient{
			ServerPort: in.PortOverride, UUID: alice.UUID, Flow: r.Flow, ServerName: domain,
			PublicKey: r.Values["REALITY_PUBLIC_KEY"].(string), ShortID: r.Values["REALITY_SHORT_ID"].(string), SendThrough: from,
		}))
	}
	phone, laptop := client("127.0.0.2"), client("127.0.0.3")
	xraytest.Eventually(t, wait, "phone connects", func() error { return phone.get(origin) })
	xraytest.Eventually(t, wait, "phone takes the slot", func() error {
		if ips := tr.Online(alice.ID); len(ips) != 1 || !ips[0].Admitted {
			return fmt.Errorf("online %+v", ips)
		}
		return nil
	})

	// The laptop gets in at first, then is blocked on the node; the phone keeps working.
	if err := laptop.get(origin); err != nil {
		t.Fatalf("the second address must get in once: %v", err)
	}
	xraytest.Eventually(t, wait, "laptop blocked", func() error {
		if err := laptop.fresh(origin); err == nil {
			return fmt.Errorf("laptop still gets through (online %+v)", tr.Online(alice.ID))
		}
		return nil
	})
	if err := phone.fresh(origin); err != nil {
		t.Fatalf("the phone is cut off too: %v", err)
	}

	// The laptop's subscription shows the limit, with the HWID-limit header.
	req, _ := http.NewRequest(http.MethodGet, "http://"+panel.subAddr+service.DefaultSubPrefix+alice.SubToken, nil)
	req.Header.Set("User-Agent", "v2rayNG/1.9")
	req.Header.Set("X-Forwarded-For", "127.0.0.3")
	resp, err := http.DefaultClient.Do(req)
	must(t, err)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	links, _ := decodeB64(string(body))
	if !strings.Contains(links, "00000000-0000-0000-0000-000000000000") || !strings.Contains(links, url.PathEscape("Превышен лимит подключений: 2/1")) || resp.Header.Get("x-hwid-max-devices-reached") != "true" {
		t.Fatalf("laptop subscription: %q %v", links, resp.Header)
	}

	// The phone leaves: after the grace period the laptop takes the slot.
	phone.hc.CloseIdleConnections()
	xraytest.Eventually(t, wait, "laptop admitted after the phone left", func() error { return laptop.fresh(origin) })
}
