package store

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddNamedMigrationContext("00025_node_notes_xray.go", upNodeNotesXray, downNodeNotesXray)
}

func upNodeNotesXray(ctx context.Context, tx *sql.Tx) error {
	if err := repairNodeWizardColumns(ctx, tx); err != nil {
		return err
	}
	return addMissingColumns(ctx, tx, "nodes",
		schemaColumn{"note", "TEXT NOT NULL DEFAULT ''"},
		schemaColumn{"xray_release", "TEXT NOT NULL DEFAULT ''"})
}

func downNodeNotesXray(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `ALTER TABLE nodes DROP COLUMN xray_release`); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `ALTER TABLE nodes DROP COLUMN note`)
	return err
}
