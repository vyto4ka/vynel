package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strconv"
	"testing"
	"time"

	"golang.org/x/net/proxy"

	"github.com/vyto4ka/vynel/internal/node/state"
	nodev1 "github.com/vyto4ka/vynel/internal/proto/vynel/node/v1"
	"github.com/vyto4ka/vynel/internal/xray/xraytest"
	"github.com/vyto4ka/vynel/internal/xrayconf"
)

// keepAlive is an HTTP client whose connection through the SOCKS proxy stays open between
// requests, so Xray sees the address online.
func keepAlive(socks int) *http.Client {
	d, _ := proxy.SOCKS5("tcp", "127.0.0.1:"+strconv.Itoa(socks), nil, proxy.Direct)
	return &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DialContext: d.(proxy.ContextDialer).DialContext}}
}

func get(hc *http.Client, url string) error {
	resp, err := hc.Get(url)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, resp.Body) // read to the end: the connection stays open for reuse
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

// A blocked address of a user is cut off on the node while the user's other address keeps
// working, even though the config ends with a catch-all rule; lifting the block lets it back.
func TestIPBlocksOnXray(t *testing.T) {
	bin := xraytest.Binary(t)
	ctx := context.Background()
	origin := xraytest.Origin(t)
	port, apiPort := xraytest.FreePort(t), xraytest.FreePort(t)
	const uuid = "11111111-2222-4333-8444-555555555555"
	cfg := map[string]any{
		"log": map[string]any{"loglevel": "warning"},
		"inbounds": []any{map[string]any{"tag": "IN", "listen": "0.0.0.0", "port": port, "protocol": "vless",
			"settings": map[string]any{"clients": []any{}, "decryption": "none"}, "streamSettings": map[string]any{"network": "raw"}}},
		"outbounds": []any{xraytest.Direct(), map[string]any{"tag": xrayconf.IPBlockTag, "protocol": "blackhole"}},
		"routing":   map[string]any{"rules": []any{map[string]any{"network": "tcp,udp", "outboundTag": "DIRECT"}}},
		"api":       map[string]any{"tag": "api", "listen": "127.0.0.1:" + strconv.Itoa(apiPort), "services": []any{"HandlerService", "StatsService", "RoutingService"}},
		"stats":     map[string]any{},
		"policy":    map[string]any{"levels": map[string]any{"0": map[string]any{"statsUserOnline": true}}},
	}
	raw, _ := json.Marshal(cfg)
	a, err := New(Config{DataDir: t.TempDir(), Xray: bin})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	st := &state.State{Config: raw, Inbounds: []state.Inbound{{Tag: "IN", Protocol: "vless", Users: []state.User{{Email: "7", ID: uuid}}}}}
	a.mu.Lock()
	a.cur = st
	err = a.restartLocked(ctx, st)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	client := func(from string) *http.Client {
		socks := xraytest.FreePort(t)
		c, _ := json.Marshal(map[string]any{
			"inbounds": []any{map[string]any{"listen": "127.0.0.1", "port": socks, "protocol": "socks", "settings": map[string]any{"udp": false}}},
			"outbounds": []any{map[string]any{"protocol": "vless", "sendThrough": from,
				"settings": map[string]any{"vnext": []any{map[string]any{"address": "127.0.0.1", "port": port,
					"users": []any{map[string]any{"id": uuid, "encryption": "none"}}}}}}},
		})
		xraytest.StartClient(t, bin, c, socks)
		return keepAlive(socks)
	}
	phone, laptop := client("127.0.0.2"), client("127.0.0.3")
	if err := get(phone, origin); err != nil {
		t.Fatal(err)
	}
	if err := get(laptop, origin); err != nil {
		t.Fatal(err)
	}
	xraytest.Eventually(t, 5*time.Second, "both addresses online", func() error {
		msg := a.onlineIPs(ctx)
		if msg == nil || len(msg.Users) != 1 || !slices.Equal(msg.Users[0].IpList, []string{"127.0.0.2", "127.0.0.3"}) {
			return fmt.Errorf("online %v", msg)
		}
		return nil
	})

	a.SetIPBlocks(ctx, []*nodev1.IPBlock{{UserId: 7, Ips: []string{"127.0.0.3"}}})
	if os.Geteuid() == 0 {
		// Its open connection is closed (ss -K needs root), so it is no longer online.
		xraytest.Eventually(t, 5*time.Second, "blocked address dropped", func() error {
			if msg := a.onlineIPs(ctx); msg == nil || len(msg.Users) != 1 || !slices.Equal(msg.Users[0].IpList, []string{"127.0.0.2"}) {
				return fmt.Errorf("online %v", msg)
			}
			return nil
		})
	}
	laptop.CloseIdleConnections()
	if err := get(laptop, origin); err == nil {
		t.Fatal("the blocked address still gets through")
	}
	phone.CloseIdleConnections()
	if err := get(phone, origin); err != nil {
		t.Fatalf("the other address is cut off too: %v", err)
	}

	a.SetIPBlocks(ctx, nil)
	laptop.CloseIdleConnections()
	if err := get(laptop, origin); err != nil {
		t.Fatalf("the address is still blocked after the block was lifted: %v", err)
	}

	// The blocks survive an Xray restart (a new config).
	a.SetIPBlocks(ctx, []*nodev1.IPBlock{{UserId: 7, Ips: []string{"127.0.0.3"}}})
	a.mu.Lock()
	err = a.restartLocked(ctx, st)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	laptop.CloseIdleConnections()
	if err := get(laptop, origin); err == nil {
		t.Fatal("the block is gone after a restart")
	}
	if err := get(phone, origin); err != nil {
		t.Fatalf("phone after restart: %v", err)
	}
}

// A node whose base config has no routing section: the panel's config still lets the agent add
// block rules (Xray's stub router refused them, and blocks silently did nothing).
func TestIPBlocksWithoutBaseRouting(t *testing.T) {
	bin := xraytest.Binary(t)
	ctx := context.Background()
	origin := xraytest.Origin(t)
	port, apiPort := xraytest.FreePort(t), xraytest.FreePort(t)
	const uuid = "11111111-2222-4333-8444-555555555555"
	base, _ := xrayconf.ParseBase(`{"outbounds":[` + xraytest.DirectJSON + `]}`)
	cfg, err := xrayconf.BuildConfig(xrayconf.NodeConfig{Base: base, APIAddr: "127.0.0.1:" + strconv.Itoa(apiPort)})
	if err != nil {
		t.Fatal(err)
	}
	cfg["inbounds"] = []any{map[string]any{"tag": "IN", "listen": "0.0.0.0", "port": port, "protocol": "vless",
		"settings": map[string]any{"clients": []any{}, "decryption": "none"}, "streamSettings": map[string]any{"network": "raw"}}}
	raw, _ := json.Marshal(cfg)
	a, err := New(Config{DataDir: t.TempDir(), Xray: bin})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	st := &state.State{Config: raw, Inbounds: []state.Inbound{{Tag: "IN", Protocol: "vless", Users: []state.User{{Email: "7", ID: uuid}}}}}
	a.mu.Lock()
	a.cur = st
	err = a.restartLocked(ctx, st)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	socks := xraytest.FreePort(t)
	c, _ := json.Marshal(map[string]any{
		"inbounds": []any{map[string]any{"listen": "127.0.0.1", "port": socks, "protocol": "socks", "settings": map[string]any{"udp": false}}},
		"outbounds": []any{map[string]any{"protocol": "vless", "sendThrough": "127.0.0.3",
			"settings": map[string]any{"vnext": []any{map[string]any{"address": "127.0.0.1", "port": port,
				"users": []any{map[string]any{"id": uuid, "encryption": "none"}}}}}}},
	})
	xraytest.StartClient(t, bin, c, socks)
	laptop := keepAlive(socks)
	if err := get(laptop, origin); err != nil {
		t.Fatal(err)
	}
	a.SetIPBlocks(ctx, []*nodev1.IPBlock{{UserId: 7, Ips: []string{"127.0.0.3"}}})
	if !a.blocksApplied {
		t.Fatal("the blocks were not applied")
	}
	laptop.CloseIdleConnections()
	if err := get(laptop, origin); err == nil {
		t.Fatal("the blocked address still gets through")
	}
}
