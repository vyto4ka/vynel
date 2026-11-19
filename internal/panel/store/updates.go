package store

import (
	"context"
	"database/sql"
	"errors"
)

// UpdateRun is the persisted state and log of the latest update for the panel (NodeID = 0)
// or a node (NodeID > 0).
type UpdateRun struct {
	NodeID      int64
	FromVersion string
	ToVersion   string
	State       string
	OK          bool
	SentAt      int64
	StartedAt   int64
	FinishedAt  int64
	DismissedAt int64
	Log         string
	UpdatedAt   int64
}

// GetUpdateRun reads the stored update run for nodeID (0 = panel). Returns nil when none exists.
func GetUpdateRun(ctx context.Context, q DBTX, nodeID int64) (*UpdateRun, error) {
	r := &UpdateRun{}
	var ok int
	err := q.QueryRowContext(ctx, `SELECT node_id, from_version, to_version, state, ok, sent_at, started_at, finished_at, dismissed_at, log, updated_at
		FROM update_runs WHERE node_id = ?`, nodeID).
		Scan(&r.NodeID, &r.FromVersion, &r.ToVersion, &r.State, &ok, &r.SentAt, &r.StartedAt, &r.FinishedAt, &r.DismissedAt, &r.Log, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	r.OK = ok != 0
	return r, err
}

// ListUpdateRuns returns all persisted update runs indexed by node_id.
func ListUpdateRuns(ctx context.Context, q DBTX) (map[int64]*UpdateRun, error) {
	rows, err := q.QueryContext(ctx, `SELECT node_id, from_version, to_version, state, ok, sent_at, started_at, finished_at, dismissed_at, log, updated_at
		FROM update_runs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]*UpdateRun{}
	for rows.Next() {
		r := &UpdateRun{}
		var ok int
		if err := rows.Scan(&r.NodeID, &r.FromVersion, &r.ToVersion, &r.State, &ok, &r.SentAt, &r.StartedAt, &r.FinishedAt, &r.DismissedAt, &r.Log, &r.UpdatedAt); err != nil {
			return nil, err
		}
		r.OK = ok != 0
		out[r.NodeID] = r
	}
	return out, rows.Err()
}

// UpsertUpdateRun writes or updates the update run for r.NodeID.
func UpsertUpdateRun(ctx context.Context, q DBTX, r UpdateRun) error {
	ok := 0
	if r.OK {
		ok = 1
	}
	_, err := q.ExecContext(ctx, `INSERT INTO update_runs (node_id, from_version, to_version, state, ok, sent_at, started_at, finished_at, dismissed_at, log, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (node_id) DO UPDATE SET
			from_version = excluded.from_version,
			to_version   = excluded.to_version,
			state        = excluded.state,
			ok           = excluded.ok,
			sent_at      = excluded.sent_at,
			started_at   = excluded.started_at,
			finished_at  = excluded.finished_at,
			dismissed_at = excluded.dismissed_at,
			log          = excluded.log,
			updated_at   = excluded.updated_at`,
		r.NodeID, r.FromVersion, r.ToVersion, r.State, ok, r.SentAt, r.StartedAt, r.FinishedAt, r.DismissedAt, r.Log, r.UpdatedAt)
	return err
}

// DismissUpdateRun marks the current update run as hidden until a newer one starts.
func DismissUpdateRun(ctx context.Context, q DBTX, nodeID, at int64) error {
	_, err := q.ExecContext(ctx, `INSERT INTO update_runs (node_id, dismissed_at, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT (node_id) DO UPDATE SET dismissed_at = excluded.dismissed_at, updated_at = excluded.updated_at`,
		nodeID, at, at)
	return err
}
