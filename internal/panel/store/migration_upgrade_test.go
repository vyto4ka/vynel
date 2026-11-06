package store

import (
	"database/sql"
	"path/filepath"
	"slices"
	"testing"

	"github.com/pressly/goose/v3"
)

// Exercise actual pre-upgrade schemas and their historical migration ledger, not
// just a fresh database. Old trial builds assigned 23 to notes and 24 to groups.
func TestUpgradeMainAndTrialSchemas(t *testing.T) {
	for _, mode := range []string{"v22", "main24", "trial-notes23", "trial-groups24", "trial-both24", "trial-notes-and-wizard24", "groups26-first", "notes25-first"} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			path := filepath.Join(t.TempDir(), "panel.db")
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			db.SetMaxOpenConns(1)
			t.Cleanup(func() { _ = db.Close() })
			goose.SetBaseFS(migrations)
			goose.SetLogger(goose.NopLogger())
			if err := goose.SetDialect("sqlite3"); err != nil {
				t.Fatal(err)
			}
			if err := goose.UpToContext(ctx, db, "migrations", 22); err != nil {
				t.Fatal(err)
			}
			exec := func(query string, args ...any) {
				t.Helper()
				if _, err := db.ExecContext(ctx, query, args...); err != nil {
					t.Fatal(err)
				}
			}
			mark := func(version int) {
				exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (?, true)`, version)
			}
			notes, defaults, manual, confirmed := false, false, 0, 0
			addNotes := func() {
				exec(`ALTER TABLE nodes ADD COLUMN note TEXT NOT NULL DEFAULT ''`)
				exec(`ALTER TABLE nodes ADD COLUMN xray_release TEXT NOT NULL DEFAULT ''`)
				notes = true
			}
			addGroups := func() {
				exec(`ALTER TABLE groups ADD COLUMN user_defaults TEXT NOT NULL DEFAULT 'null'`)
				defaults = true
			}
			switch mode {
			case "main24", "groups26-first", "notes25-first":
				if err := goose.UpToContext(ctx, db, "migrations", 24); err != nil {
					t.Fatal(err)
				}
				manual, confirmed = 1, 123456
			case "trial-notes23", "trial-both24", "trial-notes-and-wizard24":
				addNotes()
				mark(23)
			}
			switch mode {
			case "trial-groups24", "trial-both24":
				addGroups()
				mark(24)
			case "trial-notes-and-wizard24":
				exec(`ALTER TABLE nodes ADD COLUMN requirements_confirmed_at INTEGER NOT NULL DEFAULT 0`)
				mark(24)
			case "groups26-first":
				addGroups()
				mark(26)
			case "notes25-first":
				addNotes()
				mark(25)
			}
			exec(`INSERT INTO nodes (id, name, code, created_at, updated_at) VALUES (1, 'Existing node', 'DE', 10, 20)`)
			exec(`INSERT INTO groups (id, name, created_at) VALUES (1, 'Existing group', 10)`)
			exec(`INSERT INTO users (id, username, uuid, sub_token, traffic_used_bytes, lifetime_used_bytes, created_at, updated_at) VALUES (1, 'existing-user', 'existing-uuid', 'existing-token', 70, 120, 10, 20)`)
			exec(`INSERT INTO user_groups (user_id, group_id) VALUES (1, 1)`)
			if manual != 0 {
				exec(`UPDATE nodes SET manual=?, requirements_confirmed_at=? WHERE id=1`, manual, confirmed)
			}
			if notes {
				exec(`UPDATE nodes SET note='keep-private-note', xray_release='v26.9.30' WHERE id=1`)
			}
			const policy = `{"expiry":"period","days":14,"traffic":"limit","trafficLimit":100}`
			if defaults {
				exec(`UPDATE groups SET user_defaults=? WHERE id=1`, policy)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			// Reopening twice also checks that the repair is idempotent and preserves values.
			for range 2 {
				st, err := Open(ctx, path)
				if err != nil {
					t.Fatal(err)
				}
				func() {
					defer st.Close()
					n, err := GetNode(ctx, st.DB, 1)
					if err != nil || n.Name != "Existing node" || n.Manual != (manual != 0) || n.RequirementsConfirmedAt != int64(confirmed) {
						t.Fatalf("node changed: %+v / %v", n, err)
					}
					u, err := GetUser(ctx, st.DB, 1)
					if err != nil || u.Username != "existing-user" || u.TrafficUsedBytes != 70 || u.LifetimeUsedBytes != 120 {
						t.Fatalf("user changed: %+v / %v", u, err)
					}
					ids, err := UserGroupIDs(ctx, st.DB, 1)
					if err != nil || !slices.Equal(ids, []int64{1}) {
						t.Fatalf("membership changed: %v / %v", ids, err)
					}
					g, err := GetGroup(ctx, st.DB, 1)
					if err != nil || g.Name != "Existing group" {
						t.Fatalf("group changed: %+v / %v", g, err)
					}
					if notes {
						var note, release string
						if err := st.DB.QueryRowContext(ctx, `SELECT note, xray_release FROM nodes WHERE id=1`).Scan(&note, &release); err != nil || note != "keep-private-note" || release != "v26.9.30" {
							t.Fatalf("private fields changed: %q / %q / %v", note, release, err)
						}
					}
					if defaults {
						var value string
						if err := st.DB.QueryRowContext(ctx, `SELECT user_defaults FROM groups WHERE id=1`).Scan(&value); err != nil || value != policy {
							t.Fatalf("policy changed: %q / %v", value, err)
						}
					}
				}()
			}
		})
	}
}
