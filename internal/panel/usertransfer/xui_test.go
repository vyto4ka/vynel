package usertransfer

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/vyto4ka/vynel/internal/panel/store"
)

// A classic-layout 3x-ui database: clients in inbounds.settings JSON, traffic in
// client_traffics. The same person on two inbounds becomes one user.
func TestFetchXUIClassic(t *testing.T) {
	db := xuiDB(t, `
CREATE TABLE inbounds (id INTEGER PRIMARY KEY, settings TEXT);
CREATE TABLE client_traffics (email TEXT UNIQUE, up INTEGER, down INTEGER);
INSERT INTO inbounds (settings) VALUES ('{"clients":[
  {"id":"uuid-alice","email":"alice","limitIp":2,"totalGB":10737418240,"expiryTime":1798761600000,"enable":true,"tgId":777,"comment":"vip","reset":3},
  {"id":"uuid-bob","email":"bob","enable":false}]}');
INSERT INTO inbounds (settings) VALUES ('{"clients":[
  {"id":"uuid-alice2","email":"alice","limitIp":0,"totalGB":21474836480,"expiryTime":0,"enable":true}]}');
INSERT INTO client_traffics VALUES ('alice', 100, 200);
INSERT INTO client_traffics VALUES ('bob', 1, 2);
`)

	f, err := FetchXUI(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if f.Meta.Source != SourceXUI || f.Meta.Users != 2 {
		t.Fatalf("meta: %+v", f.Meta)
	}
	byName := map[string]TransferUser{}
	for _, u := range f.Users {
		byName[u.Username] = u
	}
	a := byName["alice"]
	if a.UUID != "uuid-alice" {
		t.Errorf("alice uuid: %q", a.UUID)
	}
	// Merged: the tightest limit and expiry, traffic from client_traffics.
	if a.TrafficLimitBytes == nil || *a.TrafficLimitBytes != 10737418240 {
		t.Errorf("alice limit: %v", a.TrafficLimitBytes)
	}
	if a.ExpireAt == nil || *a.ExpireAt != 1798761600 {
		t.Errorf("alice expire: %v", a.ExpireAt)
	}
	if a.TrafficUsedBytes != 300 {
		t.Errorf("alice traffic: %d", a.TrafficUsedBytes)
	}
	if a.IPLimit == nil || *a.IPLimit != 2 || a.ResetStrategy != "month" || a.Note != "vip" || a.TelegramID == nil || *a.TelegramID != 777 {
		t.Errorf("alice fields: %+v", a)
	}
	if b := byName["bob"]; b.Status != "disabled" || b.TrafficUsedBytes != 3 {
		t.Errorf("bob: %+v", b)
	}
}

// The v3 layout: a clients table, no clients inside settings.
func TestFetchXUIV3(t *testing.T) {
	db := xuiDB(t, `
CREATE TABLE inbounds (id INTEGER PRIMARY KEY, settings TEXT);
CREATE TABLE clients (email TEXT, uuid TEXT, enable INTEGER, total_gb INTEGER, expiry_time INTEGER,
  limit_ip INTEGER, tg_id INTEGER, comment TEXT, reset INTEGER);
CREATE TABLE client_traffics (email TEXT UNIQUE, up INTEGER, down INTEGER);
INSERT INTO clients VALUES ('carl','uuid-carl',1,0,0,0,0,'',0);
INSERT INTO clients VALUES ('dana','uuid-dana',0,5368709120,0,3,888,'paid',2);
INSERT INTO client_traffics VALUES ('dana', 10, 20);
`)

	f, err := FetchXUI(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if f.Meta.Users != 2 {
		t.Fatalf("meta: %+v", f.Meta)
	}
	byName := map[string]TransferUser{}
	for _, u := range f.Users {
		byName[u.Username] = u
	}
	if c := byName["carl"]; c.UUID != "uuid-carl" || c.Status != "active" || c.TrafficLimitBytes != nil {
		t.Errorf("carl: %+v", c)
	}
	d := byName["dana"]
	if d.Status != "disabled" || d.TrafficLimitBytes == nil || *d.TrafficLimitBytes != 5368709120 {
		t.Errorf("dana limit: %+v", d)
	}
	if d.TrafficUsedBytes != 30 || d.IPLimit == nil || *d.IPLimit != 3 || d.ResetStrategy != "week" {
		t.Errorf("dana: %+v", d)
	}
}

// A trojan client's password stands in for the uuid; a string tgId is read too.
func TestFetchXUITrojanAndStringTG(t *testing.T) {
	db := xuiDB(t, `
CREATE TABLE inbounds (id INTEGER PRIMARY KEY, settings TEXT);
INSERT INTO inbounds (settings) VALUES ('{"clients":[
  {"password":"tro-pass","email":"erin","enable":true,"tgId":"555"}]}');
`)
	f, err := FetchXUI(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Users) != 1 || f.Users[0].UUID != "tro-pass" {
		t.Fatalf("erin: %+v", f.Users)
	}
	if f.Users[0].TelegramID == nil || *f.Users[0].TelegramID != 555 {
		t.Errorf("erin tg: %+v", f.Users[0])
	}
}

// A missing file is an error, not an empty import.
func TestFetchXUIErrors(t *testing.T) {
	if _, err := FetchXUI(context.Background(), filepath.Join(t.TempDir(), "nope.db")); err == nil {
		t.Fatal("missing file accepted")
	}
}

// The file of a 3x-ui fetch imports.
func TestXUIFileImports(t *testing.T) {
	f := &ExportFile{Meta: ExportMeta{Format: Format, Source: SourceXUI, Users: 1},
		Users: []TransferUser{{Username: "alice", UUID: "uuid-alice", Status: store.StatusActive}}}
	st := testStore(t)
	im := NewImporter(st, ImportOptions{})
	n := 0
	im.NewTokenFn = func() (string, error) { n++; return fmt.Sprintf("minted-%d", n), nil }
	rep, err := im.Import(context.Background(), f)
	if err != nil || rep.Created != 1 {
		t.Fatalf("import: %v %+v", err, rep)
	}
	if u := mustUser(t, st, "alice"); u.SubToken == "" {
		t.Error("no token minted")
	}
}

// xuiDB makes a throwaway sqlite file with the given SQL (raw, as 3x-ui would have left it).
func xuiDB(t *testing.T, schema string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "x-ui.db")
	st, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.ExecContext(context.Background(), schema); err != nil {
		t.Fatal(err)
	}
	st.Close()
	return path
}
