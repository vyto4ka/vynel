package store

import (
	"context"
	"database/sql"
	"errors"
)

// GetSetting returns a setting value; ok is false when unset.
func GetSetting(ctx context.Context, q DBTX, key string) (value string, ok bool, err error) {
	err = q.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return value, err == nil, err
}

// SetSetting stores a setting.
func SetSetting(ctx context.Context, q DBTX, key, value string) error {
	_, err := q.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

// AuditEntry is one row of the audit log.
type AuditEntry struct {
	ID       int64
	TS       int64
	Actor    string
	ActorID  string
	Action   string
	Entity   string
	EntityID int64
	Diff     string
}

// AddAudit writes an audit entry.
func AddAudit(ctx context.Context, q DBTX, e AuditEntry) error {
	if e.Diff == "" {
		e.Diff = "{}"
	}
	_, err := q.ExecContext(ctx, `INSERT INTO audit_log (ts, actor, actor_id, action, entity, entity_id, diff) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		unix(), e.Actor, e.ActorID, e.Action, e.Entity, e.EntityID, e.Diff)
	if err != nil {
		return err
	}
	return publishAudit(ctx, q, e)
}

// ListAudit returns the newest entries first.
func ListAudit(ctx context.Context, q DBTX, limit int) ([]AuditEntry, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, ts, actor, actor_id, action, entity, coalesce(entity_id, 0), diff FROM audit_log ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.TS, &e.Actor, &e.ActorID, &e.Action, &e.Entity, &e.EntityID, &e.Diff); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Event is an outbox entry.
type Event struct {
	ID       int64
	TS       int64
	Type     string
	EntityID int64
	Payload  string
}

// AddEvent appends to the outbox.
func AddEvent(ctx context.Context, q DBTX, typ string, entityID int64, payload any) error {
	_, err := q.ExecContext(ctx, `INSERT INTO events_outbox (ts, type, entity_id, payload) VALUES (?, ?, ?, ?)`, unix(), typ, entityID, toJSON(payload))
	return err
}

// LastEventID returns the newest outbox id (0 when empty).
//
// Events that never change what nodes run (a device registered, an address over the limit) do
// not count: every new phone would make the panel recompute every node.
func LastEventID(ctx context.Context, q DBTX) (int64, error) {
	var id int64
	err := q.QueryRowContext(ctx, `SELECT id FROM events_outbox WHERE type NOT LIKE 'device.%' AND type <> 'user.ip_limit'
		ORDER BY id DESC LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

// ListEventsAfter returns events with id > after.
func ListEventsAfter(ctx context.Context, q DBTX, after int64, limit int) ([]Event, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, ts, type, coalesce(entity_id, 0), payload FROM events_outbox WHERE id > ? ORDER BY id LIMIT ?`, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.TS, &e.Type, &e.EntityID, &e.Payload); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ListSettings returns all stored settings.
func ListSettings(ctx context.Context, q DBTX) (map[string]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// AllSettings returns every setting.
func AllSettings(ctx context.Context, q DBTX) (map[string]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}
