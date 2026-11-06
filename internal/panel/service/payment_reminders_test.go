package service

import (
	"testing"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/store"
)

func TestPaymentReminderRetryRestartAndStaleAcknowledgement(t *testing.T) {
	f := newFixture(t)
	n, _ := must2(f.s.CreateNode(f.ctx, ActorCLI, NodeInput{Name: "Due", Country: "de"}))
	until := f.now.Add(24 * time.Hour).Unix()
	must(f.s.SetNodeBilling(f.ctx, ActorCLI, n.ID, NodeBillingInput{PaidUntil: &until}))
	// A full notification channel must not permanently lose the reminder.
	for range 32 {
		f.s.notify(Notice{Kind: "test"})
	}
	must0(t, f.s.checkBilling(f.ctx))
	drainNotices(f.s)
	must0(t, f.s.checkBilling(f.ctx))
	if ns := drainNotices(f.s); len(ns) != 1 || ns[0].PaidUntil != until || ns[0].NodeID != n.ID {
		t.Fatalf("not queued after pressure: %+v", ns)
	}
	f.now = f.now.Add(paymentRetry + time.Second)
	must0(t, f.s.checkBilling(f.ctx))
	if len(drainNotices(f.s)) != 1 {
		t.Fatal("undelivered reminder not retried")
	}
	// The retry state is expendable: the database still knows it is undelivered.
	restarted := New(f.s.st)
	restarted.SetClock(func() time.Time { return f.now })
	must0(t, restarted.checkBilling(f.ctx))
	if len(drainNotices(restarted)) != 1 {
		t.Fatal("restart lost the reminder")
	}
	later := until + 86400
	must(f.s.SetNodeBilling(f.ctx, ActorCLI, n.ID, NodeBillingInput{PaidUntil: &later}))
	must0(t, f.s.MarkPaymentReminderSent(f.ctx, n.ID, until))
	if got := must(store.GetNodeBilling(f.ctx, f.s.st.DB, n.ID)); got.RemindedFor != 0 {
		t.Fatal("old send marked a new period")
	}
	if msg := must(f.s.PaymentReminder(f.ctx, n.ID, until)); msg != nil {
		t.Fatal("stale queued reminder survived renewal")
	}
	stale := must(store.GetNodeBilling(f.ctx, f.s.st.DB, n.ID))
	must0(t, f.s.MarkPaymentReminderSent(f.ctx, n.ID, later))
	// A traffic-limit save based on an older snapshot cannot undo delivery.
	must0(t, store.SaveNodeBilling(f.ctx, f.s.st.DB, stale))
	if got := must(store.GetNodeBilling(f.ctx, f.s.st.DB, n.ID)); got.RemindedFor != later {
		t.Fatal("billing save lost acknowledgement")
	}
	if msg := must(f.s.PaymentReminder(f.ctx, n.ID, later)); msg != nil {
		t.Fatal("delivered reminder remains pending")
	}
}
