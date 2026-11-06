package store

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddNamedMigrationContext("00026_group_defaults.go", upGroupDefaults, downGroupDefaults)
}

func upGroupDefaults(ctx context.Context, tx *sql.Tx) error {
	if err := repairNodeWizardColumns(ctx, tx); err != nil {
		return err
	}
	return addMissingColumns(ctx, tx, "groups", schemaColumn{"user_defaults", "TEXT NOT NULL DEFAULT 'null'"})
}

func downGroupDefaults(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `ALTER TABLE groups DROP COLUMN user_defaults`)
	return err
}
