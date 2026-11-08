package store

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
)

func TestPanelRentalMigrationKeepsTrafficAndReminder(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "panel.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	goose.SetBaseFS(migrations)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpToContext(ctx, db, "migrations", 24); err != nil {
		t.Fatal(err)
	}
	exec := func(s string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO providers(id,name,created_at) VALUES(1,'Hosting',1)`)
	exec(`INSERT INTO nodes(id,name,code,local,created_at,updated_at) VALUES(1,'Local','LOCAL',1,1,1),(2,'Remote','DE',0,1,1)`)
	exec(`INSERT INTO node_billing(node_id,provider_id,cost,currency,cost_period,paid_until,reminded_for,traffic_limit,reset_day,traffic_count,off_period,override_period) VALUES(1,1,1200,'€','year',1800000000,1800000000,100000,15,'out',123,456),(2,1,9,'€','month',1800000001,0,0,1,'both',0,0)`)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		st, err := Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		p, err := GetPanelBilling(ctx, st.DB)
		if err != nil {
			t.Fatal(err)
		}
		if p.Cost != 1200 || p.CostPeriod != "year" || p.ProviderID == nil || *p.ProviderID != 1 || p.RemindedFor != 1800000000 || *p.PaidUntil != 1800000000 {
			t.Fatalf("rental lost: %+v", p)
		}
		local, err := GetNodeBilling(ctx, st.DB, 1)
		if err != nil {
			t.Fatal(err)
		}
		if local.Cost != 0 || local.ProviderID != nil || local.PaidUntil != nil || local.TrafficLimit != 100000 || local.ResetDay != 15 || local.TrafficCount != "out" || local.OffPeriod != 123 || local.OverridePeriod != 456 {
			t.Fatalf("local traffic or duplicate rental: %+v", local)
		}
		remote, err := GetNodeBilling(ctx, st.DB, 2)
		if err != nil || remote.Cost != 9 || remote.PaidUntil == nil || *remote.PaidUntil != 1800000001 {
			t.Fatalf("remote changed: %+v / %v", remote, err)
		}
		st.Close()
	}
}
