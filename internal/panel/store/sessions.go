package store

import (
	"context"
	"database/sql"
)

// WebSession is a signed-in browser.
type WebSession struct {
	ID         string
	Method     string
	Actor      string
	IP         string
	UserAgent  string
	CreatedAt  int64
	LastSeenAt int64
	ExpiresAt  int64
	RevokedAt  *int64
}

const webSessionCols = `id, method, actor, ip, user_agent, created_at, last_seen_at, expires_at, revoked_at`

func scanWebSession(sc interface{ Scan(...any) error }) (*WebSession, error) {
	w := &WebSession{}
	var rev sql.NullInt64
	if err := sc.Scan(&w.ID, &w.Method, &w.Actor, &w.IP, &w.UserAgent, &w.CreatedAt, &w.LastSeenAt, &w.ExpiresAt, &rev); err != nil {
		return nil, mapErr(err)
	}
	w.RevokedAt = ptrInt(rev)
	return w, nil
}

// CreateWebSession stores a new session.
func CreateWebSession(ctx context.Context, q DBTX, w *WebSession) error {
	_, err := q.ExecContext(ctx, `INSERT INTO web_sessions (`+webSessionCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL)`,
		w.ID, w.Method, w.Actor, w.IP, w.UserAgent, w.CreatedAt, w.LastSeenAt, w.ExpiresAt)
	return mapErr(err)
}

// GetWebSession finds a session by id.
func GetWebSession(ctx context.Context, q DBTX, id string) (*WebSession, error) {
	return scanWebSession(q.QueryRowContext(ctx, `SELECT `+webSessionCols+` FROM web_sessions WHERE id=?`, id))
}

// TouchWebSession records activity.
func TouchWebSession(ctx context.Context, q DBTX, id string, now int64, ip string) error {
	_, err := q.ExecContext(ctx, `UPDATE web_sessions SET last_seen_at=?, ip=? WHERE id=?`, now, ip, id)
	return err
}

// ListWebSessions returns sessions that are neither revoked nor expired, newest activity first.
func ListWebSessions(ctx context.Context, q DBTX, now int64) ([]*WebSession, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+webSessionCols+` FROM web_sessions WHERE revoked_at IS NULL AND expires_at>? ORDER BY last_seen_at DESC`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*WebSession
	for rows.Next() {
		w, err := scanWebSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// RevokeWebSessions ends one session (id) or every session except keep (id == "").
func RevokeWebSessions(ctx context.Context, q DBTX, id, keep string, now int64) (int64, error) {
	var res sql.Result
	var err error
	if id != "" {
		res, err = q.ExecContext(ctx, `UPDATE web_sessions SET revoked_at=? WHERE id=? AND revoked_at IS NULL`, now, id)
	} else {
		res, err = q.ExecContext(ctx, `UPDATE web_sessions SET revoked_at=? WHERE id<>? AND revoked_at IS NULL`, now, keep)
	}
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// PruneWebSessions forgets sessions that ended more than a month ago.
func PruneWebSessions(ctx context.Context, q DBTX, before int64) error {
	_, err := q.ExecContext(ctx, `DELETE FROM web_sessions WHERE expires_at<? OR revoked_at<?`, before, before)
	return err
}
