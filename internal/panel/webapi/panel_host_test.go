package webapi

import (
	"encoding/json"
	"testing"

	"github.com/vyto4ka/vynel/internal/panel/service"
)

func TestPanelHostRentalAPIWithoutNode(t *testing.T) {
	c, svc, pw := newServer(t)
	if w := c.do("PATCH", "/api/self-host/billing", `{"cost":9}`, true); w.Code != 401 {
		t.Fatal("unauthenticated rental")
	}
	c.do("POST", "/api/login", `{"login":"boss","password":"`+pw+`"}`, true)
	if w := c.do("PATCH", "/api/self-host/billing", `{"cost":9}`, false); w.Code != 403 {
		t.Fatal("missing csrf")
	}
	if w := c.do("PATCH", "/api/self-host/billing", `{"cost":1200,"costPeriod":"year","note":"Private host note"}`, true); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var host struct {
		Domains  []string
		WithNode bool
		Billing  PanelBilling
	}
	w := c.do("GET", "/api/self-host", "", false)
	if err := json.Unmarshal(w.Body.Bytes(), &host); err != nil {
		t.Fatal(err)
	}
	if host.Domains == nil || host.WithNode || host.Billing.Cost != 1200 || host.Billing.MonthlyCost != 100 || host.Billing.Note != "Private host note" {
		t.Fatalf("host: %s", w.Body.String())
	}
	if nodes, err := svc.Nodes(t.Context()); err != nil || len(nodes) != 0 {
		t.Fatal("panel host created a fake node")
	}
	n, err := svc.EnsureLocalNode(t.Context(), service.NodeInput{})
	if err != nil {
		t.Fatal(err)
	}
	w = c.do("PATCH", "/api/nodes/"+itoa(n.ID)+"/billing", `{"cost":2400,"trafficLimit":10000}`, true)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = c.do("GET", "/api/billing", "", false)
	var bill struct {
		Panel PanelBilling
		Nodes []struct {
			Local        bool
			Cost         float64
			TrafficLimit int64
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &bill); err != nil {
		t.Fatal(err)
	}
	if bill.Panel.Cost != 2400 || bill.Panel.MonthlyCost != 200 || len(bill.Nodes) != 1 || !bill.Nodes[0].Local || bill.Nodes[0].Cost != 2400 || bill.Nodes[0].TrafficLimit != 10000 {
		t.Fatalf("billing: %s", w.Body.String())
	}
}
