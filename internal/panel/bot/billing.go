package bot

import (
	"context"

	"github.com/vyto4ka/vynel/internal/panel/service"
)

type paymentDelivery struct {
	until  int64
	admins map[int64]bool
}

func (b *Bot) onBilling(ctx context.Context, notice service.Notice) {
	if notice.PaidUntil == 0 {
		b.toAdmins(ctx, notice.Text) // traffic-limit notices have no payment acknowledgement
		return
	}
	api := b.client()
	if api == nil {
		return
	}
	n, err := b.svc.PaymentReminder(ctx, notice.NodeID, notice.PaidUntil)
	if err != nil {
		b.log.Warn("payment reminder lookup", "node", notice.NodeID, "err", err)
		return
	}
	if n == nil {
		if b.paymentSent[notice.NodeID].until == notice.PaidUntil {
			delete(b.paymentSent, notice.NodeID)
		}
		return
	}
	admins, err := b.svc.BotAdmins(ctx)
	if err != nil || len(admins) == 0 {
		return
	}
	progress := b.paymentSent[n.NodeID]
	if progress.until != n.PaidUntil {
		progress = paymentDelivery{until: n.PaidUntil, admins: map[int64]bool{}}
	}
	var keys Keyboard
	if n.ActionURL != "" {
		keys = Keyboard{{Button{Text: "💳 Кабинет провайдера", URL: n.ActionURL}}}
	}
	all := true
	for _, a := range admins {
		if progress.admins[a.ID] {
			continue
		}
		if _, err := api.send(ctx, a.ID, n.Text, keys); err != nil {
			b.log.Warn("payment reminder delivery", "node", n.NodeID, "admin", a.ID, "err", err)
			all = false
		} else {
			progress.admins[a.ID] = true
		}
	}
	if b.paymentSent == nil {
		b.paymentSent = map[int64]paymentDelivery{}
	}
	b.paymentSent[n.NodeID] = progress
	if all {
		if err := b.svc.MarkPaymentReminderSent(ctx, n.NodeID, n.PaidUntil); err != nil {
			b.log.Warn("payment reminder acknowledgement", "node", n.NodeID, "err", err)
			return
		}
		delete(b.paymentSent, n.NodeID)
	}
}
