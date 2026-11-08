package service

import (
	"context"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/store"
)

const paymentRetry = 5 * time.Minute

type paymentAttempt struct {
	until int64
	at    time.Time
}

// Queueing is only an attempt, not delivery. Unacknowledged reminders survive a
// stopped bot or a panel restart because reminded_for stays unset in the database.
func (s *Service) queuePaymentReminder(ctx context.Context, n *store.Node, b *store.NodeBilling) {
	s.paymentMu.Lock()
	defer s.paymentMu.Unlock()
	prev := s.paymentAttempts[n.ID]
	if prev.until == *b.PaidUntil && s.now().Sub(prev.at) < paymentRetry {
		return
	}
	if s.tryNotify(s.paymentNotice(ctx, n, b)) {
		if s.paymentAttempts == nil {
			s.paymentAttempts = map[int64]paymentAttempt{}
		}
		s.paymentAttempts[n.ID] = paymentAttempt{until: *b.PaidUntil, at: s.now()}
	}
}

func (s *Service) paymentNotice(ctx context.Context, n *store.Node, b *store.NodeBilling) Notice {
	link := ""
	if b.ProviderID != nil {
		if p, err := store.GetProvider(ctx, s.st.DB, *b.ProviderID); err == nil {
			link = firstNonEmpty(p.LoginURL, p.URL)
		}
	}
	return Notice{Kind: "billing", NodeID: n.ID, PaidUntil: *b.PaidUntil, ActionURL: link, Text: s.payText(ctx, n, b)}
}

// PaymentReminder rebuilds the message just before sending, using the current
// cost, provider and days left. Renewed/deleted/already delivered entries vanish.
func (s *Service) PaymentReminder(ctx context.Context, nodeID, until int64) (*Notice, error) {
	b, err := store.GetNodeBilling(ctx, s.st.DB, nodeID)
	if err != nil {
		return nil, err
	}
	if b.PaidUntil == nil || *b.PaidUntil != until || b.RemindedFor == until || time.Unix(until, 0).Sub(s.now()) > PayReminder {
		return nil, nil
	}
	n, err := store.GetNode(ctx, s.st.DB, nodeID)
	if err != nil {
		return nil, err
	}
	msg := s.paymentNotice(ctx, n, b)
	return &msg, nil
}

// MarkPaymentReminderSent cannot mark a newly renewed period as already reminded.
func (s *Service) MarkPaymentReminderSent(ctx context.Context, nodeID, until int64) error {
	_, err := s.st.DB.ExecContext(ctx, `UPDATE node_billing SET reminded_for=? WHERE node_id=? AND paid_until=?`, until, nodeID, until)
	return err
}
