package store

import "context"

// ProfileTemplate is a template written in the panel.
type ProfileTemplate struct {
	ID        string
	Source    string
	Rev       int64
	CreatedAt int64
	UpdatedAt int64
}

// ListProfileTemplates returns all custom templates.
func ListProfileTemplates(ctx context.Context, q DBTX) ([]*ProfileTemplate, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, source, rev, created_at, updated_at FROM profile_templates ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ProfileTemplate
	for rows.Next() {
		t := &ProfileTemplate{}
		if err := rows.Scan(&t.ID, &t.Source, &t.Rev, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ProfileTemplatesStamp changes whenever a custom template is added, edited or removed.
func ProfileTemplatesStamp(ctx context.Context, q DBTX) (int64, error) {
	var n, rev int64
	err := q.QueryRowContext(ctx, `SELECT count(*), coalesce(sum(rev), 0) FROM profile_templates`).Scan(&n, &rev)
	return n<<32 | rev, err
}

// SaveProfileTemplate inserts or updates a template.
func SaveProfileTemplate(ctx context.Context, q DBTX, id, source string) error {
	now := unix()
	_, err := q.ExecContext(ctx, `INSERT INTO profile_templates (id, source, rev, created_at, updated_at) VALUES (?, ?, 1, ?, ?)
		ON CONFLICT (id) DO UPDATE SET source=excluded.source, rev=profile_templates.rev+1, updated_at=excluded.updated_at`, id, source, now, now)
	return mapErr(err)
}

// DeleteProfileTemplate removes a template.
func DeleteProfileTemplate(ctx context.Context, q DBTX, id string) error {
	return execOne(ctx, q, `DELETE FROM profile_templates WHERE id=?`, id)
}
