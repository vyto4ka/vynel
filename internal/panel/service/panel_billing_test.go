package service

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/store"
)

func TestPanelRentalWithoutNodeAndSharedLocalRental(t *testing.T) {
	f := newFixture(t)
	p := must(f.s.CreateProvider(f.ctx, ActorCLI, ProviderInput{Name: "Hosting"}))
	cost, cur, period, note := 1200.0, "₽", "year", "Private panel note"
	until := f.now.Add(24 * time.Hour).Unix()
	must(f.s.SetPanelBilling(f.ctx, ActorCLI, PanelBillingInput{ProviderID: &p.ID, Cost: &cost, Currency: &cur, CostPeriod: &period, PaidUntil: &until, Note: &note}))
	if len(must(f.s.Nodes(f.ctx))) != 0 {
		t.Fatal("panel rental created a fake node")
	}
	bill := must(f.s.PanelBilling(f.ctx))
	if bill.MonthlyCost != 100 || bill.LocalNodeID != nil || bill.Provider.Name != "Hosting" {
		t.Fatalf("panel only: %+v", bill)
	}
	n := must(f.s.EnsureLocalNode(f.ctx, NodeInput{Name: "Local VPN"}))
	fee := must(f.s.BillingOverview(f.ctx))
	if len(fee) != 1 || fee[0].Billing.Cost != cost || fee[0].Billing.CostPeriod != "year" {
		t.Fatalf("local rental not shared: %+v", fee)
	}
	providers := must(f.s.NodeProviders(f.ctx))
	if providers[n.ID] == nil || providers[n.ID].ID != p.ID {
		t.Fatal("local provider not shared")
	}
	newCost, limit := 2400.0, int64(100<<30)
	must(f.s.SetNodeBilling(f.ctx, ActorCLI, n.ID, NodeBillingInput{Cost: &newCost, TrafficLimit: &limit}))
	bill = must(f.s.PanelBilling(f.ctx))
	if bill.Billing.Cost != newCost || bill.MonthlyCost != 200 || bill.Billing.Note != note {
		t.Fatal("editing local fee did not update panel")
	}
	stored := must(store.GetNodeBilling(f.ctx, f.s.st.DB, n.ID))
	if stored.Cost != 0 || stored.PaidUntil != nil || stored.ProviderID != nil || stored.TrafficLimit != limit {
		t.Fatal("second rental stored or VPN traffic lost")
	}
	must(f.s.SetPanelBilling(f.ctx, ActorCLI, PanelBillingInput{Cost: &cost}))
	if got := must(f.s.BillingOverview(f.ctx)); got[0].Billing.Cost != cost || got[0].Billing.TrafficLimit != limit {
		t.Fatal("panel edit changed traffic / local view")
	}
	// Turning off or removing the VPN role cannot delete the server's rental.
	f.s.SelfWithNode = func() bool { return false }
	if must(f.s.SelfHost(f.ctx)).WithNode {
		t.Fatal("stored inactive node became a running role")
	}
	must0(t, f.s.DeleteNode(f.ctx, ActorCLI, n.ID))
	if got := must(f.s.PanelBilling(f.ctx)); got.LocalNodeID != nil || got.Billing.Cost != cost || got.Billing.Note != note {
		t.Fatal("removing VPN role removed rental")
	}
	must0(t, f.s.DeleteProvider(f.ctx, ActorCLI, p.ID))
	if got := must(f.s.PanelBilling(f.ctx)); got.Provider != nil || got.Billing.ProviderID != nil || got.Billing.Cost != cost {
		t.Fatal("deleting provider changed the rental")
	}
}

func TestPanelPaymentRetryAndRenewal(t *testing.T) {
	f := newFixture(t)
	until, cost, note := f.now.Add(24*time.Hour).Unix(), 9.5, "do not send private note"
	must(f.s.SetPanelBilling(f.ctx, ActorCLI, PanelBillingInput{PaidUntil: &until, Cost: &cost, Note: &note}))
	for range 32 {
		f.s.notify(Notice{Kind: "test"})
	}
	must0(t, f.s.checkPanelPayment(f.ctx))
	drainNotices(f.s)
	must0(t, f.s.checkPanelPayment(f.ctx))
	ns := drainNotices(f.s)
	if len(ns) != 1 || ns[0].Kind != "panel-billing" || ns[0].PanelPayment.PaidUntil != until {
		t.Fatalf("queue: %+v", ns)
	}
	msg := must(f.s.PanelPaymentReminder(f.ctx, until))
	if msg == nil || !strings.Contains(msg.Text, "сервера панели") || !strings.Contains(msg.Text, "9.50 ₽ в месяц") || strings.Contains(msg.Text, note) {
		t.Fatalf("message: %+v", msg)
	}
	if must(store.GetPanelBilling(f.ctx, f.s.st.DB)).RemindedFor != 0 {
		t.Fatal("queueing was delivery")
	}
	f.now = f.now.Add(6 * time.Minute)
	must0(t, f.s.checkPanelPayment(f.ctx))
	if len(drainNotices(f.s)) != 1 {
		t.Fatal("failed delivery not retried")
	}
	restarted := New(f.s.st)
	restarted.SetClock(func() time.Time { return f.now })
	must0(t, restarted.checkPanelPayment(f.ctx))
	if len(drainNotices(restarted)) != 1 {
		t.Fatal("restart lost pending payment")
	}
	later := until + 86400
	must(f.s.SetPanelBilling(f.ctx, ActorCLI, PanelBillingInput{PaidUntil: &later}))
	must0(t, f.s.MarkPanelPaymentSent(f.ctx, until))
	if must(f.s.PanelPaymentReminder(f.ctx, until)) != nil || must(store.GetPanelBilling(f.ctx, f.s.st.DB)).RemindedFor != 0 {
		t.Fatal("old reminder survived renewal")
	}
	must0(t, f.s.MarkPanelPaymentSent(f.ctx, later))
	must(f.s.SetPanelBilling(f.ctx, ActorCLI, PanelBillingInput{Note: &note}))
	if must(store.GetPanelBilling(f.ctx, f.s.st.DB)).RemindedFor != later {
		t.Fatal("ordinary save lost delivery state")
	}
	if len(must(f.s.Nodes(f.ctx))) != 0 {
		t.Fatal("payment processing created a node")
	}
}

func TestPanelRentalValidation(t *testing.T) {
	f := newFixture(t)
	cost, cur, period, note := -1.0, "", "week", strings.Repeat("x", 4001)
	for _, in := range []PanelBillingInput{{Cost: &cost}, {Currency: &cur}, {CostPeriod: &period}, {Note: &note}} {
		if _, err := f.s.SetPanelBilling(f.ctx, ActorCLI, in); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid rental accepted: %v", err)
		}
	}
	if got := must(store.GetPanelBilling(f.ctx, f.s.st.DB)); got.Cost != 0 || got.Note != "" || got.Currency != "₽" {
		t.Fatal("invalid input partially saved")
	}
}
