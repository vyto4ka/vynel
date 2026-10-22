package agent

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/vyto4ka/vynel/internal/node/state"
	nodev1 "github.com/vyto4ka/vynel/internal/proto/vynel/node/v1"
	"github.com/vyto4ka/vynel/internal/xray/xraytest"
)

// A user whose email changes but whose UUID stays (as when the panel started naming clients
// per inbound) keeps connecting. Xray removes a VLESS user by email and forgets its UUID with
// it, so the old email has to go before the new one comes, whatever order the panel sends.
func TestRenamedUserKeepsWorking(t *testing.T) {
	bin := xraytest.Binary(t)
	ctx := context.Background()
	origin := xraytest.Origin(t)
	port, apiPort := xraytest.FreePort(t), xraytest.FreePort(t)
	const uuid = "11111111-2222-4333-8444-555555555555"
	cfg := map[string]any{
		"log": map[string]any{"loglevel": "warning"},
		"inbounds": []any{map[string]any{"tag": "IN", "listen": "127.0.0.1", "port": port, "protocol": "vless",
			"settings": map[string]any{"clients": []any{}, "decryption": "none"}, "streamSettings": map[string]any{"network": "raw"}}},
		"outbounds": []any{map[string]any{"tag": "DIRECT", "protocol": "freedom"}},
		"api":       map[string]any{"tag": "api", "listen": "127.0.0.1:" + strconv.Itoa(apiPort), "services": []any{"HandlerService", "StatsService"}},
		"stats":     map[string]any{},
	}
	raw, _ := json.Marshal(cfg)
	a, err := New(Config{DataDir: t.TempDir(), Xray: bin})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	st := &state.State{Config: raw, Hash: "h1", Inbounds: []state.Inbound{{Tag: "IN", Protocol: "vless", Users: []state.User{{Email: "7", ID: uuid}}}}}
	a.mu.Lock()
	a.cur = st
	err = a.restartLocked(ctx, st)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	works := func(what string) {
		t.Helper()
		socks := xraytest.FreePort(t)
		c, _ := json.Marshal(map[string]any{
			"inbounds": []any{map[string]any{"listen": "127.0.0.1", "port": socks, "protocol": "socks", "settings": map[string]any{"udp": false}}},
			"outbounds": []any{map[string]any{"protocol": "vless",
				"settings": map[string]any{"vnext": []any{map[string]any{"address": "127.0.0.1", "port": port,
					"users": []any{map[string]any{"id": uuid, "encryption": "none"}}}}}}},
		})
		xraytest.StartClient(t, bin, c, socks)
		if err := get(keepAlive(socks), origin); err != nil {
			t.Fatalf("%s: the user cannot connect: %v", what, err)
		}
	}
	works("before")

	// The order an older panel sent: the new email first, then the old one removed.
	err = a.ApplyDelta(ctx, &nodev1.Delta{BaseHash: "h1", Hash: "h2", Revision: 2, Ops: []*nodev1.UserOp{
		{Kind: nodev1.UserOp_KIND_UPSERT, Tag: "IN", User: &nodev1.User{Email: "7.i1", Id: uuid}},
		{Kind: nodev1.UserOp_KIND_REMOVE, Tag: "IN", User: &nodev1.User{Email: "7"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	works("after a delta")

	// The same through a full user sync (a snapshot without a restart).
	a.mu.Lock()
	next := a.cur.Clone()
	next.Inbounds[0].Users = []state.User{{Email: "7.i2", ID: uuid}}
	err = a.syncUsersLocked(ctx, a.cur, next)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	works("after a sync")
}
