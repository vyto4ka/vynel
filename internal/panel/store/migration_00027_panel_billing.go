package store

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddNamedMigrationContext("00027_panel_billing.go", upPanelBilling, downPanelBilling)
}

func upPanelBilling(ctx context.Context, tx *sql.Tx) error {
	if err := repairNodeWizardColumns(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `CREATE TABLE panel_billing (
		id INTEGER PRIMARY KEY CHECK(id=1),
		provider_id INTEGER REFERENCES providers(id) ON DELETE SET NULL,
		cost REAL NOT NULL DEFAULT 0,
		currency TEXT NOT NULL DEFAULT '₽',
		cost_period TEXT NOT NULL DEFAULT 'month',
		paid_until INTEGER,
		reminded_for INTEGER NOT NULL DEFAULT 0,
		note TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		return err
	}
	// Transfer only the rental; the local VPN's traffic limit and state stay on the node.
	if _, err := tx.ExecContext(ctx, `INSERT INTO panel_billing (id, provider_id, cost, currency, cost_period, paid_until, reminded_for)
		SELECT 1, b.provider_id, b.cost, b.currency, b.cost_period, b.paid_until, b.reminded_for
		FROM node_billing b JOIN nodes n ON n.id=b.node_id WHERE n.local=1 ORDER BY n.id LIMIT 1`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO panel_billing (id) VALUES (1)`); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE node_billing SET provider_id=NULL, cost=0, paid_until=NULL, reminded_for=0 WHERE node_id IN (SELECT id FROM nodes WHERE local=1)`)
	return err
}

func downPanelBilling(ctx context.Context, tx *sql.Tx) error {
	// Restore the common rental to an existing local node for the older schema.
	if _, err := tx.ExecContext(ctx, `INSERT INTO node_billing (node_id, provider_id, cost, currency, cost_period, paid_until, reminded_for)
		SELECT n.id, p.provider_id, p.cost, p.currency, p.cost_period, p.paid_until, p.reminded_for
		FROM nodes n JOIN panel_billing p ON p.id=1 WHERE n.local=1
		ON CONFLICT(node_id) DO UPDATE SET provider_id=excluded.provider_id, cost=excluded.cost, currency=excluded.currency,
		cost_period=excluded.cost_period, paid_until=excluded.paid_until, reminded_for=excluded.reminded_for`); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DROP TABLE panel_billing`)
	return err
}
