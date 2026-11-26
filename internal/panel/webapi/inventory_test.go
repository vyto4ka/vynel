package webapi

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/vyto4ka/vynel/internal/node/inventory"
	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
)

func TestNodeInventoryEndpoint(t *testing.T) {
	c, svc, pw := newServer(t)
	if w := c.do("POST", "/api/login", `{"login":"boss","password":"`+pw+`"}`, true); w.Code != 200 {
		t.Fatal("login")
	}
	ctx := context.Background()
	n, _, err := svc.CreateNode(ctx, service.ActorCLI, service.NodeInput{Name: "DE", Country: "de", Domain: "de.example.com"})
	if err != nil {
		t.Fatal(err)
	}

	// Old/offline node without reported inventory yet: supported=false, snapshot=nil.
	w := c.do("GET", fmt.Sprintf("/api/nodes/%d/inventory", n.ID), "", false)
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var view service.NodeInventoryView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.Supported || view.Snapshot != nil {
		t.Fatalf("expected unsupported empty view, got %+v", view)
	}

	// Persisted Hello inventory is served even when offline.
	initial := inventory.Snapshot{
		CollectedAt: 1700000000,
		OS:          inventory.OSInfo{Distro: "Ubuntu 24.04.2 LTS", Kernel: "6.8.0-51-generic", Arch: "amd64", InstalledAt: 1690000000, Uptime: 3600},
		Hardware:    inventory.HardwareInfo{CPUModel: "AMD EPYC 9654", CPUCores: 4, MemTotal: 8 << 30, MemUsed: 2 << 30},
		Interfaces:  []inventory.NetInterface{{Name: "eth0", Kind: "physical", Up: true, MTU: 1500, Primary: true, Addresses: []string{"203.0.113.10/24"}}},
	}
	raw, _ := json.Marshal(initial)
	if err := svc.SaveNodeInventory(ctx, n.ID, raw); err != nil {
		t.Fatal(err)
	}
	if err := store.SetNodeFacts(ctx, svc.Store().DB, n.ID, store.NodeFacts{Capabilities: []string{"inventory"}}); err != nil {
		t.Fatal(err)
	}

	w = c.do("GET", fmt.Sprintf("/api/nodes/%d/inventory", n.ID), "", false)
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil || !view.Supported || view.Live || view.Snapshot == nil || view.Snapshot.OS.Distro != "Ubuntu 24.04.2 LTS" {
		t.Fatalf("cached inventory mismatch: %+v, err=%v", view, err)
	}

	// Connected node with live refresh returns updated snapshot.
	svc.NodeConnected = func(id int64) bool { return id == n.ID }
	svc.NodeInventoryFetch = func(_ context.Context, id int64) (*inventory.Snapshot, error) {
		fresh := initial
		fresh.CollectedAt = 1700000500
		fresh.OS.Uptime = 4100
		return &fresh, nil
	}
	w = c.do("GET", fmt.Sprintf("/api/nodes/%d/inventory?live=1", n.ID), "", false)
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil || !view.Live || view.Snapshot == nil || view.Snapshot.OS.Uptime != 4100 {
		t.Fatalf("live inventory mismatch: %+v, err=%v", view, err)
	}

	// Deleting the node cleans up its cached inventory setting.
	if err := svc.DeleteNode(ctx, service.ActorCLI, n.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := store.GetSetting(ctx, svc.Store().DB, fmt.Sprintf("node.inventory.%d", n.ID)); ok {
		t.Fatal("node inventory setting was not cleaned up on DeleteNode")
	}
}
