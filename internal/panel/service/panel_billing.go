package service

import (
	"context"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/vyto4ka/vynel/internal/panel/store"
)

type PanelBillingInput struct {
	ProviderID *int64   `json:"providerId"`
	Cost       *float64 `json:"cost"`
	Currency   *string  `json:"currency"`
	CostPeriod *string  `json:"costPeriod"`
	PaidUntil  *int64   `json:"paidUntil"`
	Note       *string  `json:"note"`
}

type PanelBill struct {
	Billing     *store.PanelBilling
	Provider    *store.Provider
	LocalNodeID *int64
	MonthlyCost float64
}

func rentalMonthly(cost float64, period string) float64 {
	if period == "year" {
		return math.Round(cost/12*100) / 100
	}
	return cost
}

func (s *Service) PanelBilling(ctx context.Context) (*PanelBill, error) {
	b, err := store.GetPanelBilling(ctx, s.st.DB)
	if err != nil {
		return nil, err
	}
	out := &PanelBill{Billing: b, MonthlyCost: rentalMonthly(b.Cost, b.CostPeriod)}
	if b.ProviderID != nil {
		out.Provider, err = store.GetProvider(ctx, s.st.DB, *b.ProviderID)
		if err != nil {
			return nil, err
		}
	}
	nodes, err := store.ListNodes(ctx, s.st.DB)
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		if n.Local {
			id := n.ID
			out.LocalNodeID = &id
			break
		}
	}
	return out, nil
}

// applyRentalInput uses the same validation for both the panel and VPN node forms.
func applyRentalInput(ctx context.Context, q store.DBTX, b *store.NodeBilling, in NodeBillingInput) error {
	if in.ProviderID != nil {
		b.ProviderID = nil
		if *in.ProviderID != 0 {
			if _, err := store.GetProvider(ctx, q, *in.ProviderID); err != nil {
				return invalid("провайдер #%d не найден", *in.ProviderID)
			}
			id := *in.ProviderID
			b.ProviderID = &id
		}
	}
	if in.Cost != nil {
		if *in.Cost < 0 || math.IsNaN(*in.Cost) || *in.Cost > 1e9 {
			return InField("Стоимость", invalid("нужно число от 0"))
		}
		b.Cost = math.Round(*in.Cost*100) / 100
	}
	if in.Currency != nil {
		c := strings.TrimSpace(*in.Currency)
		if c == "" || len([]rune(c)) > 8 {
			return InField("Валюта", invalid("от 1 до 8 символов"))
		}
		b.Currency = c
	}
	if in.CostPeriod != nil {
		if *in.CostPeriod != "month" && *in.CostPeriod != "year" {
			return InField("Период оплаты", invalid("month или year"))
		}
		b.CostPeriod = *in.CostPeriod
	}
	if in.PaidUntil != nil {
		if *in.PaidUntil <= 0 {
			b.PaidUntil = nil
			b.RemindedFor = 0
		} else if b.PaidUntil == nil || *b.PaidUntil != *in.PaidUntil {
			v := *in.PaidUntil
			b.PaidUntil = &v
			b.RemindedFor = 0
		}
	}
	return nil
}

func panelRentalIntoNode(p *store.PanelBilling, b *store.NodeBilling) {
	b.ProviderID, b.Cost, b.Currency, b.CostPeriod, b.PaidUntil, b.RemindedFor = p.ProviderID, p.Cost, p.Currency, p.CostPeriod, p.PaidUntil, p.RemindedFor
}

func nodeRentalIntoPanel(b *store.NodeBilling, p *store.PanelBilling) {
	p.ProviderID, p.Cost, p.Currency, p.CostPeriod, p.PaidUntil, p.RemindedFor = b.ProviderID, b.Cost, b.Currency, b.CostPeriod, b.PaidUntil, b.RemindedFor
}

func (s *Service) SetPanelBilling(ctx context.Context, actor Actor, in PanelBillingInput) (*store.PanelBilling, error) {
	var p *store.PanelBilling
	err := s.mutate(ctx, change{actor: actor, action: "panel.billing", entity: "panel", diff: in}, func(q store.DBTX) error {
		var err error
		p, err = store.GetPanelBilling(ctx, q)
		if err != nil {
			return err
		}
		b := &store.NodeBilling{}
		panelRentalIntoNode(p, b)
		if err := applyRentalInput(ctx, q, b, NodeBillingInput{ProviderID: in.ProviderID, Cost: in.Cost, Currency: in.Currency, CostPeriod: in.CostPeriod, PaidUntil: in.PaidUntil}); err != nil {
			return err
		}
		nodeRentalIntoPanel(b, p)
		if in.Note != nil {
			if utf8.RuneCountInString(*in.Note) > 4000 {
				return invalid("примечание: не более 4000 символов")
			}
			p.Note = strings.TrimSpace(*in.Note)
		}
		return store.SavePanelBilling(ctx, q, p)
	})
	return p, err
}
