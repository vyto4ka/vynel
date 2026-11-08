package service

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/store"
)

type PanelPayment struct {
	PaidUntil       int64
	Text, ActionURL string
}

// PanelPaymentReminder resolves again before delivery; a renewed period invalidates
// old queued notices. A failed send never marks the date as already reminded.
func (s *Service) PanelPaymentReminder(ctx context.Context, until int64) (*PanelPayment, error) {
	p, err := s.PanelBilling(ctx)
	if err != nil {
		return nil, err
	}
	b := p.Billing
	if b.PaidUntil == nil || *b.PaidUntil != until || b.RemindedFor == until || time.Unix(until, 0).Sub(s.now()) > PayReminder {
		return nil, nil
	}
	days := int(math.Ceil(time.Unix(until, 0).Sub(s.now()).Hours() / 24))
	when := fmt.Sprintf("через %d дн.", days)
	if days < 0 {
		when = "оплата просрочена"
	} else if days == 0 {
		when = "сегодня"
	}
	text := fmt.Sprintf("💳 Оплата <b>сервера панели</b>: оплачен до %s — %s", time.Unix(until, 0).In(s.BotLocation(ctx)).Format("02.01.2006"), when)
	if b.Cost > 0 {
		period := " в месяц"
		if b.CostPeriod == "year" {
			period = " в год"
		}
		text += fmt.Sprintf("\nСтоимость: %s %s", money(b.Cost), esc(b.Currency)) + period
	}
	link := ""
	if p.Provider != nil {
		text += "\nПровайдер: " + esc(p.Provider.Name)
		link = firstNonEmpty(p.Provider.LoginURL, p.Provider.URL)
	}
	return &PanelPayment{PaidUntil: until, Text: text, ActionURL: link}, nil
}

func (s *Service) checkPanelPayment(ctx context.Context) error {
	b, err := store.GetPanelBilling(ctx, s.st.DB)
	if err != nil {
		return err
	}
	if b.PaidUntil == nil || b.RemindedFor == *b.PaidUntil || time.Unix(*b.PaidUntil, 0).Sub(s.now()) > PayReminder {
		return nil
	}
	s.panelPaymentMu.Lock()
	defer s.panelPaymentMu.Unlock()
	if s.panelPaymentAttempt == *b.PaidUntil && s.now().Sub(s.panelPaymentAt) < 5*time.Minute {
		return nil
	}
	select {
	case s.noticeCh() <- Notice{Kind: "panel-billing", PanelPayment: &PanelPayment{PaidUntil: *b.PaidUntil}}:
		s.panelPaymentAttempt, s.panelPaymentAt = *b.PaidUntil, s.now()
	default:
	}
	return nil
}

func (s *Service) MarkPanelPaymentSent(ctx context.Context, until int64) error {
	_, err := s.st.DB.ExecContext(ctx, `UPDATE panel_billing SET reminded_for=? WHERE id=1 AND paid_until=?`, until, until)
	return err
}
