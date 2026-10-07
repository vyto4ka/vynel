package store

import "context"

// AddLoginToken stores a login token hash and drops expired ones.
func AddLoginToken(ctx context.Context, q DBTX, hash, issuedBy string, expiresAt int64) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM login_tokens WHERE expires_at < ?`, unix()-3600); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, `INSERT INTO login_tokens (hash, issued_by, expires_at) VALUES (?, ?, ?)`, hash, issuedBy, expiresAt)
	return mapErr(err)
}

// UseLoginToken marks an unused, unexpired token as used and returns who issued it.
// ErrNotFound means the token is unknown, expired or already used.
func UseLoginToken(ctx context.Context, q DBTX, hash string, now int64) (string, error) {
	var issuedBy string
	err := q.QueryRowContext(ctx, `UPDATE login_tokens SET used_at=? WHERE hash=? AND used_at IS NULL AND expires_at >= ? RETURNING issued_by`,
		now, hash, now).Scan(&issuedBy)
	return issuedBy, mapErr(err)
}
