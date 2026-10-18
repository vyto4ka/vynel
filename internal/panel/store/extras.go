package store

import (
	"context"
	"database/sql"
)

// Cascade sends traffic into one node inbound and out through another node's inbound
// (docs/USER_GUIDE.md §5.2).
type Cascade struct {
	ID             int64
	Name           string
	EntryInboundID int64
	ExitInboundID  int64
	Remark         string // the name in apps; "" = "<entry> → <exit>"
	ServiceUUID    string // the entry node's user on the exit inbound
	Enabled        bool
	CreatedAt      int64
}

const cascadeCols = `id, name, entry_inbound_id, exit_inbound_id, remark, service_uuid, enabled, created_at`

func scanCascade(sc interface{ Scan(...any) error }) (*Cascade, error) {
	c := &Cascade{}
	if err := sc.Scan(&c.ID, &c.Name, &c.EntryInboundID, &c.ExitInboundID, &c.Remark, &c.ServiceUUID, &c.Enabled, &c.CreatedAt); err != nil {
		return nil, mapErr(err)
	}
	return c, nil
}

// CreateCascade inserts a cascade.
func CreateCascade(ctx context.Context, q DBTX, c *Cascade) error {
	c.CreatedAt = unix()
	res, err := q.ExecContext(ctx, `INSERT INTO cascades (name, entry_inbound_id, exit_inbound_id, remark, service_uuid, enabled, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, c.Name, c.EntryInboundID, c.ExitInboundID, c.Remark, c.ServiceUUID, c.Enabled, c.CreatedAt)
	if err != nil {
		return mapErr(err)
	}
	c.ID, err = res.LastInsertId()
	return err
}

// UpdateCascade saves a cascade.
func UpdateCascade(ctx context.Context, q DBTX, c *Cascade) error {
	return execOne(ctx, q, `UPDATE cascades SET name=?, entry_inbound_id=?, exit_inbound_id=?, remark=?, enabled=? WHERE id=?`,
		c.Name, c.EntryInboundID, c.ExitInboundID, c.Remark, c.Enabled, c.ID)
}

// GetCascade loads a cascade.
func GetCascade(ctx context.Context, q DBTX, id int64) (*Cascade, error) {
	return scanCascade(q.QueryRowContext(ctx, `SELECT `+cascadeCols+` FROM cascades WHERE id=?`, id))
}

// ListCascades returns all cascades.
func ListCascades(ctx context.Context, q DBTX) ([]*Cascade, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+cascadeCols+` FROM cascades ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Cascade
	for rows.Next() {
		c, err := scanCascade(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DeleteCascade removes a cascade and who had it.
func DeleteCascade(ctx context.Context, q DBTX, id int64) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM grants WHERE kind=? AND ref_id=?`, GrantCascade, id); err != nil {
		return err
	}
	return execOne(ctx, q, `DELETE FROM cascades WHERE id=?`, id)
}

// ExternalSub is a subscription link whose servers are merged into ours (docs/USER_GUIDE.md §5.3).
type ExternalSub struct {
	ID        int64
	Name      string
	URL       string
	Enabled   bool
	Links     string // the last good copy: one link per line
	FetchedAt *int64
	Error     string // the last fetch error ("" = fine)
	CreatedAt int64
}

const externalCols = `id, name, url, enabled, links, fetched_at, error, created_at`

func scanExternal(sc interface{ Scan(...any) error }) (*ExternalSub, error) {
	e := &ExternalSub{}
	var at sql.NullInt64
	if err := sc.Scan(&e.ID, &e.Name, &e.URL, &e.Enabled, &e.Links, &at, &e.Error, &e.CreatedAt); err != nil {
		return nil, mapErr(err)
	}
	e.FetchedAt = ptrInt(at)
	return e, nil
}

// CreateExternalSub inserts an external subscription.
func CreateExternalSub(ctx context.Context, q DBTX, e *ExternalSub) error {
	e.CreatedAt = unix()
	res, err := q.ExecContext(ctx, `INSERT INTO external_subs (name, url, enabled, links, fetched_at, error, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		e.Name, e.URL, e.Enabled, e.Links, nullInt(e.FetchedAt), e.Error, e.CreatedAt)
	if err != nil {
		return mapErr(err)
	}
	e.ID, err = res.LastInsertId()
	return err
}

// UpdateExternalSub saves name, link and switch.
func UpdateExternalSub(ctx context.Context, q DBTX, e *ExternalSub) error {
	return execOne(ctx, q, `UPDATE external_subs SET name=?, url=?, enabled=? WHERE id=?`, e.Name, e.URL, e.Enabled, e.ID)
}

// SetExternalFetch records a fetch: new links (when err is "") or the error, keeping the last
// good links.
func SetExternalFetch(ctx context.Context, q DBTX, id int64, links, fetchErr string) error {
	if fetchErr != "" {
		_, err := q.ExecContext(ctx, `UPDATE external_subs SET error=? WHERE id=?`, fetchErr, id)
		return err
	}
	_, err := q.ExecContext(ctx, `UPDATE external_subs SET links=?, fetched_at=?, error='' WHERE id=?`, links, unix(), id)
	return err
}

// GetExternalSub loads an external subscription.
func GetExternalSub(ctx context.Context, q DBTX, id int64) (*ExternalSub, error) {
	return scanExternal(q.QueryRowContext(ctx, `SELECT `+externalCols+` FROM external_subs WHERE id=?`, id))
}

// ListExternalSubs returns all external subscriptions.
func ListExternalSubs(ctx context.Context, q DBTX) ([]*ExternalSub, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+externalCols+` FROM external_subs ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ExternalSub
	for rows.Next() {
		e, err := scanExternal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// DeleteExternalSub removes an external subscription and who had it.
func DeleteExternalSub(ctx context.Context, q DBTX, id int64) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM grants WHERE kind=? AND ref_id=?`, GrantExternal, id); err != nil {
		return err
	}
	return execOne(ctx, q, `DELETE FROM external_subs WHERE id=?`, id)
}

// Grant kinds.
const (
	GrantCascade  = "cascade"
	GrantExternal = "external"
)

// Grant gives a cascade or an external subscription to a user or to everyone in a group.
type Grant struct {
	ID        int64
	UserID    *int64
	GroupID   *int64
	Kind      string
	RefID     int64
	ExpireAt  *int64 // nil = for as long as the user has the subscription
	CreatedAt int64
}

const grantCols = `id, user_id, group_id, kind, ref_id, expire_at, created_at`

func scanGrant(sc interface{ Scan(...any) error }) (*Grant, error) {
	g := &Grant{}
	var user, group, exp sql.NullInt64
	if err := sc.Scan(&g.ID, &user, &group, &g.Kind, &g.RefID, &exp, &g.CreatedAt); err != nil {
		return nil, mapErr(err)
	}
	g.UserID, g.GroupID, g.ExpireAt = ptrInt(user), ptrInt(group), ptrInt(exp)
	return g, nil
}

// PutGrant adds a grant or changes the end date of an existing one.
func PutGrant(ctx context.Context, q DBTX, g *Grant) error {
	g.CreatedAt = unix()
	col := "user_id"
	if g.GroupID != nil {
		col = "group_id"
	}
	err := q.QueryRowContext(ctx, `INSERT INTO grants (user_id, group_id, kind, ref_id, expire_at, created_at) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (`+col+`, kind, ref_id) WHERE `+col+` IS NOT NULL DO UPDATE SET expire_at=excluded.expire_at RETURNING id`,
		nullInt(g.UserID), nullInt(g.GroupID), g.Kind, g.RefID, nullInt(g.ExpireAt), g.CreatedAt).Scan(&g.ID)
	return mapErr(err)
}

// DeleteGrant removes a grant.
func DeleteGrant(ctx context.Context, q DBTX, id int64) error {
	return execOne(ctx, q, `DELETE FROM grants WHERE id=?`, id)
}

// GetGrant loads a grant.
func GetGrant(ctx context.Context, q DBTX, id int64) (*Grant, error) {
	return scanGrant(q.QueryRowContext(ctx, `SELECT `+grantCols+` FROM grants WHERE id=?`, id))
}

// ListGrants returns every grant.
func ListGrants(ctx context.Context, q DBTX) ([]*Grant, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+grantCols+` FROM grants ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Grant
	for rows.Next() {
		g, err := scanGrant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
