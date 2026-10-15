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
	// LastActiveAt is the last user action (a click, a key), not background polling; 0 = none
	// since sign-in.
	LastActiveAt int64
	ClosingAt    *int64 // the page reported it was closing; any later request clears it
	EndReason    string
	Path         string // the session's own URL path ("/x7.../"); "" = the panel's fixed path
}

// ActiveAt is when the admin last did something in the session.
func (w *WebSession) ActiveAt() int64 { return max(w.CreatedAt, w.LastActiveAt) }

const webSessionCols = `id, method, actor, ip, user_agent, created_at, last_seen_at, expires_at, revoked_at, last_active_at, closing_at, end_reason, path`

func scanWebSession(sc interface{ Scan(...any) error }) (*WebSession, error) {
	w := &WebSession{}
	var rev, closing sql.NullInt64
	if err := sc.Scan(&w.ID, &w.Method, &w.Actor, &w.IP, &w.UserAgent, &w.CreatedAt, &w.LastSeenAt, &w.ExpiresAt, &rev,
		&w.LastActiveAt, &closing, &w.EndReason, &w.Path); err != nil {
		return nil, mapErr(err)
	}
	w.RevokedAt, w.ClosingAt = ptrInt(rev), ptrInt(closing)
	return w, nil
}

// CreateWebSession stores a new session.
func CreateWebSession(ctx context.Context, q DBTX, w *WebSession) error {
	_, err := q.ExecContext(ctx, `INSERT INTO web_sessions (`+webSessionCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL, ?, NULL, '', ?)`,
		w.ID, w.Method, w.Actor, w.IP, w.UserAgent, w.CreatedAt, w.LastSeenAt, w.ExpiresAt, w.LastActiveAt, w.Path)
	return mapErr(err)
}

// GetWebSession finds a session by id.
func GetWebSession(ctx context.Context, q DBTX, id string) (*WebSession, error) {
	return scanWebSession(q.QueryRowContext(ctx, `SELECT `+webSessionCols+` FROM web_sessions WHERE id=?`, id))
}

// TouchWebSession records a request: the page is alive (it clears a closing mark) and, when
// active, the admin did something.
func TouchWebSession(ctx context.Context, q DBTX, id string, now int64, active bool) error {
	if active {
		_, err := q.ExecContext(ctx, `UPDATE web_sessions SET last_seen_at=?, last_active_at=?, closing_at=NULL WHERE id=?`, now, now, id)
		return err
	}
	_, err := q.ExecContext(ctx, `UPDATE web_sessions SET last_seen_at=?, closing_at=NULL WHERE id=?`, now, id)
	return err
}

// SetWebSessionIP records the address a session is used from now.
func SetWebSessionIP(ctx context.Context, q DBTX, id, ip string) error {
	_, err := q.ExecContext(ctx, `UPDATE web_sessions SET ip=? WHERE id=?`, ip, id)
	return err
}

// MarkWebSessionClosing records that the page is being closed.
func MarkWebSessionClosing(ctx context.Context, q DBTX, id string, now int64) error {
	_, err := q.ExecContext(ctx, `UPDATE web_sessions SET closing_at=? WHERE id=? AND revoked_at IS NULL`, now, id)
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

// RevokeWebSession ends a session; false if it had ended already.
func RevokeWebSession(ctx context.Context, q DBTX, id, reason string, now int64) (bool, error) {
	res, err := q.ExecContext(ctx, `UPDATE web_sessions SET revoked_at=?, end_reason=? WHERE id=? AND revoked_at IS NULL`, now, reason, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// AddWebSessionNotice remembers a bot message about a sign-in.
func AddWebSessionNotice(ctx context.Context, q DBTX, session string, chat, msg int64) error {
	_, err := q.ExecContext(ctx, `INSERT OR IGNORE INTO web_session_notices (session_id, chat_id, message_id) VALUES (?, ?, ?)`, session, chat, msg)
	return err
}

// TakeWebSessionNotices returns and forgets the bot messages about a sign-in, as chat/message pairs.
func TakeWebSessionNotices(ctx context.Context, q DBTX, session string) ([][2]int64, error) {
	rows, err := q.QueryContext(ctx, `SELECT chat_id, message_id FROM web_session_notices WHERE session_id=?`, session)
	if err != nil {
		return nil, err
	}
	var out [][2]int64
	for rows.Next() {
		var p [2]int64
		if err := rows.Scan(&p[0], &p[1]); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	_, err = q.ExecContext(ctx, `DELETE FROM web_session_notices WHERE session_id=?`, session)
	return out, err
}

// EndedWebSessionsWithNotices lists ended sessions whose sign-in messages are still in chats.
func EndedWebSessionsWithNotices(ctx context.Context, q DBTX, now int64) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT DISTINCT n.session_id FROM web_session_notices n
		LEFT JOIN web_sessions w ON w.id = n.session_id
		WHERE w.id IS NULL OR w.revoked_at IS NOT NULL OR w.expires_at <= ?`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// PruneWebSessions forgets sessions that ended more than a month ago.
func PruneWebSessions(ctx context.Context, q DBTX, before int64) error {
	_, err := q.ExecContext(ctx, `DELETE FROM web_sessions WHERE expires_at<? OR revoked_at<?`, before, before)
	return err
}

// WebPathInUse reports whether a session path belongs to a live session (not revoked, not
// ended, not expired).
func WebPathInUse(ctx context.Context, q DBTX, path string, now int64) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM web_sessions WHERE path=? AND revoked_at IS NULL AND end_reason='' AND expires_at > ?`,
		path, now).Scan(&n)
	return n > 0, err
}
