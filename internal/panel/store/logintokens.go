package store

import "context"

// AddLoginToken stores a login token hash (with the URL path it opens, "" = the fixed one) and
// drops expired ones.
func AddLoginToken(ctx context.Context, q DBTX, hash, issuedBy, path string, expiresAt int64) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM login_tokens WHERE expires_at < ?`, unix()-3600); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, `INSERT INTO login_tokens (hash, issued_by, expires_at, path) VALUES (?, ?, ?, ?)`, hash, issuedBy, expiresAt, path)
	return mapErr(err)
}

// UseLoginToken marks an unused, unexpired token as used and returns who issued it and its
// path. ErrNotFound means the token is unknown, expired or already used.
func UseLoginToken(ctx context.Context, q DBTX, hash string, now int64) (issuedBy, path string, err error) {
	err = q.QueryRowContext(ctx, `UPDATE login_tokens SET used_at=? WHERE hash=? AND used_at IS NULL AND expires_at >= ? RETURNING issued_by, path`,
		now, hash, now).Scan(&issuedBy, &path)
	return issuedBy, path, mapErr(err)
}

// LoginPathPending reports whether an unused, unexpired login token opens this path.
func LoginPathPending(ctx context.Context, q DBTX, path string, now int64) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM login_tokens WHERE path=? AND used_at IS NULL AND expires_at >= ?`, path, now).Scan(&n)
	return n > 0, err
}
