package store

import "context"

// PanelBilling belongs to the physical panel server, whether or not it runs a VPN node.
type PanelBilling struct {
	ProviderID  *int64  `json:"providerId"`
	Cost        float64 `json:"cost"`
	Currency    string  `json:"currency"`
	CostPeriod  string  `json:"costPeriod"`
	PaidUntil   *int64  `json:"paidUntil"`
	RemindedFor int64   `json:"-"`
	Note        string  `json:"note"`
}

func GetPanelBilling(ctx context.Context, q DBTX) (*PanelBilling, error) {
	b := &PanelBilling{}
	err := q.QueryRowContext(ctx, `SELECT provider_id, cost, currency, cost_period, paid_until, reminded_for, note FROM panel_billing WHERE id=1`).Scan(&b.ProviderID, &b.Cost, &b.Currency, &b.CostPeriod, &b.PaidUntil, &b.RemindedFor, &b.Note)
	return b, mapErr(err)
}

func SavePanelBilling(ctx context.Context, q DBTX, b *PanelBilling) error {
	return execOne(ctx, q, `UPDATE panel_billing SET provider_id=?, cost=?, currency=?, cost_period=?,
		reminded_for=CASE WHEN paid_until IS ? THEN max(reminded_for, ?) ELSE ? END,
		paid_until=?, note=? WHERE id=1`, b.ProviderID, b.Cost, b.Currency, b.CostPeriod, b.PaidUntil, b.RemindedFor, b.RemindedFor, b.PaidUntil, b.Note)
}
