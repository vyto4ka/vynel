package bot

import "context"

type panelPaymentDelivery struct {
	bot    string
	until  int64
	admins map[int64]bool
}

func (b *Bot) onPanelPayment(ctx context.Context, until int64) {
	api := b.client()
	if api == nil {
		return
	}
	n, err := b.svc.PanelPaymentReminder(ctx, until)
	if err != nil {
		b.log.Warn("panel payment reminder", "err", err)
		return
	}
	if n == nil {
		return
	}
	admins, err := b.svc.BotAdmins(ctx)
	if err != nil || len(admins) == 0 {
		return
	}
	progress := &b.panelPayment
	if progress.bot != api.base || progress.until != until {
		*progress = panelPaymentDelivery{bot: api.base, until: until, admins: map[int64]bool{}}
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
			all = false
			b.log.Warn("panel payment delivery", "admin", a.ID, "err", err)
		} else {
			progress.admins[a.ID] = true
		}
	}
	if all {
		if err := b.svc.MarkPanelPaymentSent(ctx, until); err != nil {
			b.log.Warn("panel payment acknowledgement", "err", err)
		}
	}
}
