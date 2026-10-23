package store

import (
	"context"
	"database/sql"
	"errors"
)

// Provider is a hosting provider the nodes are rented from (docs/USER_GUIDE.md §9.1).
type Provider struct {
	ID        int64
	Name      string
	URL       string
	LoginURL  string
	Icon      string // data: URI, "" = none
	CreatedAt int64
}

const providerCols = `id, name, url, login_url, icon, created_at`

func scanProvider(sc interface{ Scan(...any) error }) (*Provider, error) {
	p := &Provider{}
	if err := sc.Scan(&p.ID, &p.Name, &p.URL, &p.LoginURL, &p.Icon, &p.CreatedAt); err != nil {
		return nil, mapErr(err)
	}
	return p, nil
}

// CreateProvider inserts a provider.
func CreateProvider(ctx context.Context, q DBTX, p *Provider) error {
	p.CreatedAt = unix()
	res, err := q.ExecContext(ctx, `INSERT INTO providers (name, url, login_url, icon, created_at) VALUES (?, ?, ?, ?, ?)`,
		p.Name, p.URL, p.LoginURL, p.Icon, p.CreatedAt)
	if err != nil {
		return mapErr(err)
	}
	p.ID, err = res.LastInsertId()
	return err
}

// UpdateProvider saves a provider.
func UpdateProvider(ctx context.Context, q DBTX, p *Provider) error {
	return execOne(ctx, q, `UPDATE providers SET name=?, url=?, login_url=?, icon=? WHERE id=?`, p.Name, p.URL, p.LoginURL, p.Icon, p.ID)
}

// GetProvider reads a provider.
func GetProvider(ctx context.Context, q DBTX, id int64) (*Provider, error) {
	return scanProvider(q.QueryRowContext(ctx, `SELECT `+providerCols+` FROM providers WHERE id=?`, id))
}

// ListProviders lists the providers by name.
func ListProviders(ctx context.Context, q DBTX) ([]*Provider, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+providerCols+` FROM providers ORDER BY name COLLATE NOCASE, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Provider
	for rows.Next() {
		p, err := scanProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// DeleteProvider removes a provider; its nodes keep their billing without one.
func DeleteProvider(ctx context.Context, q DBTX, id int64) error {
	return execOne(ctx, q, `DELETE FROM providers WHERE id=?`, id)
}

// NodeBilling is what a node costs and how much traffic it may use.
type NodeBilling struct {
	NodeID         int64
	ProviderID     *int64
	Cost           float64
	Currency       string
	CostPeriod     string // month | year
	PaidUntil      *int64
	RemindedFor    int64
	TrafficLimit   int64 // bytes per period, 0 = none
	ResetDay       int
	TrafficCount   string // both | out | in
	OffPeriod      int64  // period start for which the limit switched the node off
	OverridePeriod int64  // the period the admin switched it back on anyway
}

const billingCols = `node_id, provider_id, cost, currency, cost_period, paid_until, reminded_for, traffic_limit, reset_day,
	traffic_count, off_period, override_period`

func scanBilling(sc interface{ Scan(...any) error }) (*NodeBilling, error) {
	b := &NodeBilling{}
	if err := sc.Scan(&b.NodeID, &b.ProviderID, &b.Cost, &b.Currency, &b.CostPeriod, &b.PaidUntil, &b.RemindedFor, &b.TrafficLimit,
		&b.ResetDay, &b.TrafficCount, &b.OffPeriod, &b.OverridePeriod); err != nil {
		return nil, mapErr(err)
	}
	return b, nil
}

// DefaultBilling is a node's billing before anything is set.
func DefaultBilling(nodeID int64) *NodeBilling {
	return &NodeBilling{NodeID: nodeID, Currency: "₽", CostPeriod: "month", ResetDay: 1, TrafficCount: "both"}
}

// GetNodeBilling reads a node's billing (the default when none is stored).
func GetNodeBilling(ctx context.Context, q DBTX, nodeID int64) (*NodeBilling, error) {
	b, err := scanBilling(q.QueryRowContext(ctx, `SELECT `+billingCols+` FROM node_billing WHERE node_id=?`, nodeID))
	if errors.Is(err, ErrNotFound) {
		return DefaultBilling(nodeID), nil
	}
	return b, err
}

// ListNodeBilling reads the billing of every node that has one, by node id.
func ListNodeBilling(ctx context.Context, q DBTX) (map[int64]*NodeBilling, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+billingCols+` FROM node_billing`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]*NodeBilling{}
	for rows.Next() {
		b, err := scanBilling(rows)
		if err != nil {
			return nil, err
		}
		out[b.NodeID] = b
	}
	return out, rows.Err()
}

// SaveNodeBilling stores a node's billing.
func SaveNodeBilling(ctx context.Context, q DBTX, b *NodeBilling) error {
	_, err := q.ExecContext(ctx, `INSERT INTO node_billing (`+billingCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (node_id) DO UPDATE SET provider_id=excluded.provider_id, cost=excluded.cost, currency=excluded.currency,
		cost_period=excluded.cost_period, paid_until=excluded.paid_until, reminded_for=excluded.reminded_for,
		traffic_limit=excluded.traffic_limit, reset_day=excluded.reset_day, traffic_count=excluded.traffic_count,
		off_period=excluded.off_period, override_period=excluded.override_period`,
		b.NodeID, b.ProviderID, b.Cost, b.Currency, b.CostPeriod, b.PaidUntil, b.RemindedFor, b.TrafficLimit, b.ResetDay,
		b.TrafficCount, b.OffPeriod, b.OverridePeriod)
	return mapErr(err)
}

// SetNodeEnabled switches a node on or off.
func SetNodeEnabled(ctx context.Context, q DBTX, id int64, on bool) error {
	return execOne(ctx, q, `UPDATE nodes SET enabled=? WHERE id=?`, on, id)
}

// AddIfaceTraffic adds bytes that went through a node's network interface in the hour of ts.
func AddIfaceTraffic(ctx context.Context, q DBTX, nodeID, ts, rx, tx int64) error {
	_, err := q.ExecContext(ctx, `INSERT INTO node_iface_hourly (node_id, hour, rx, tx) VALUES (?, ?, ?, ?)
		ON CONFLICT (node_id, hour) DO UPDATE SET rx = rx + excluded.rx, tx = tx + excluded.tx`, nodeID, Hour(ts), rx, tx)
	return err
}

// IfaceTraffic sums a node's interface traffic since from (an hour boundary).
func IfaceTraffic(ctx context.Context, q DBTX, nodeID, from int64) (rx, tx int64, err error) {
	err = q.QueryRowContext(ctx, `SELECT coalesce(sum(rx), 0), coalesce(sum(tx), 0) FROM node_iface_hourly WHERE node_id=? AND hour >= ?`,
		nodeID, Hour(from)).Scan(&rx, &tx)
	return rx, tx, err
}

// LastMetricsAt is the time of a node's latest metrics sample before ts (0 = none).
func LastMetricsAt(ctx context.Context, q DBTX, nodeID, ts int64) (int64, error) {
	var at sql.NullInt64
	err := q.QueryRowContext(ctx, `SELECT max(ts) FROM node_metrics WHERE node_id=? AND ts < ?`, nodeID, ts).Scan(&at)
	return at.Int64, err
}
