package store

import (
	"context"
	"database/sql"
	"fmt"
)

type schemaColumn struct{ name, definition string }

// addMissingColumns is only for trusted, compiled-in migration definitions. SQLite
// has no ADD COLUMN IF NOT EXISTS; inspect inside the migration's transaction so
// upgrading a trial database preserves columns and values it already contains.
func addMissingColumns(ctx context.Context, tx *sql.Tx, table string, columns ...schemaColumn) error {
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info(%q)`, table))
	if err != nil {
		return err
	}
	present := map[string]bool{}
	for rows.Next() {
		var cid, notNull, pk int
		var name, kind string
		var def any
		if err := rows.Scan(&cid, &name, &kind, &notNull, &def, &pk); err != nil {
			rows.Close()
			return err
		}
		present[name] = true
	}
	err = rows.Err()
	if closeErr := rows.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	for _, c := range columns {
		if present[c.name] {
			continue
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE %q ADD COLUMN %q %s`, table, c.name, c.definition)); err != nil {
			return fmt.Errorf("add %s.%s: %w", table, c.name, err)
		}
	}
	return nil
}

// Trial builds used versions 23/24 for other features before main used those
// numbers for the node wizard. Keep their migration history and data intact, and
// repair any missing wizard columns in the new forward migration instead.
func repairNodeWizardColumns(ctx context.Context, tx *sql.Tx) error {
	return addMissingColumns(ctx, tx, "nodes",
		schemaColumn{"manual", "INTEGER NOT NULL DEFAULT 0"},
		schemaColumn{"requirements_confirmed_at", "INTEGER NOT NULL DEFAULT 0"})
}
