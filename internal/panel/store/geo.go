package store

import (
	"context"
)

// GeoFile stores metadata for one panel-managed GeoSite or GeoIP (.dat) file.
type GeoFile struct {
	ID               int64
	Name             string
	Title            string
	Kind             string // "geosite" | "geoip"
	IsSystemOverride bool
	SourceURL        string
	AutoUpdate       bool
	SHA256           string
	SizeBytes        int64
	CategoriesCount  int
	EntriesCount     int
	FetchedAt        int64
	LastError        string
	Enabled          bool
	ClientExpose     bool
	ClientAlias      string
	CreatedAt        int64
	UpdatedAt        int64
}

// ListGeoFiles returns all geo_files ordered by system overrides first, then name.
func ListGeoFiles(ctx context.Context, q DBTX) ([]*GeoFile, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT id, name, title, kind, is_system_override, source_url, auto_update,
		       sha256, size_bytes, categories_count, entries_count, fetched_at,
		       last_error, enabled, client_expose, client_alias, created_at, updated_at
		FROM geo_files
		ORDER BY is_system_override DESC, name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*GeoFile
	for rows.Next() {
		g, err := scanGeoFile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// GetGeoFile loads one geo_file by id.
func GetGeoFile(ctx context.Context, q DBTX, id int64) (*GeoFile, error) {
	row := q.QueryRowContext(ctx, `
		SELECT id, name, title, kind, is_system_override, source_url, auto_update,
		       sha256, size_bytes, categories_count, entries_count, fetched_at,
		       last_error, enabled, client_expose, client_alias, created_at, updated_at
		FROM geo_files WHERE id = ?`, id)
	return scanGeoFile(row)
}

// GetGeoFileByName loads one geo_file by filename.
func GetGeoFileByName(ctx context.Context, q DBTX, name string) (*GeoFile, error) {
	row := q.QueryRowContext(ctx, `
		SELECT id, name, title, kind, is_system_override, source_url, auto_update,
		       sha256, size_bytes, categories_count, entries_count, fetched_at,
		       last_error, enabled, client_expose, client_alias, created_at, updated_at
		FROM geo_files WHERE name = ?`, name)
	return scanGeoFile(row)
}

// CreateGeoFile inserts a new geo_file record.
func CreateGeoFile(ctx context.Context, q DBTX, g *GeoFile) error {
	now := unix()
	g.CreatedAt = now
	g.UpdatedAt = now
	res, err := q.ExecContext(ctx, `
		INSERT INTO geo_files (
			name, title, kind, is_system_override, source_url, auto_update,
			sha256, size_bytes, categories_count, entries_count, fetched_at,
			last_error, enabled, client_expose, client_alias, created_at, updated_at
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		g.Name, g.Title, g.Kind, boolToInt(g.IsSystemOverride), g.SourceURL, boolToInt(g.AutoUpdate),
		g.SHA256, g.SizeBytes, g.CategoriesCount, g.EntriesCount, g.FetchedAt,
		g.LastError, boolToInt(g.Enabled), boolToInt(g.ClientExpose), g.ClientAlias, g.CreatedAt, g.UpdatedAt,
	)
	if err != nil {
		return mapErr(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	g.ID = id
	return nil
}

// UpdateGeoFile updates an existing geo_file record.
func UpdateGeoFile(ctx context.Context, q DBTX, g *GeoFile) error {
	g.UpdatedAt = unix()
	res, err := q.ExecContext(ctx, `
		UPDATE geo_files
		SET title = ?, kind = ?, is_system_override = ?, source_url = ?, auto_update = ?,
		    sha256 = ?, size_bytes = ?, categories_count = ?, entries_count = ?, fetched_at = ?,
		    last_error = ?, enabled = ?, client_expose = ?, client_alias = ?, updated_at = ?
		WHERE id = ?`,
		g.Title, g.Kind, boolToInt(g.IsSystemOverride), g.SourceURL, boolToInt(g.AutoUpdate),
		g.SHA256, g.SizeBytes, g.CategoriesCount, g.EntriesCount, g.FetchedAt,
		g.LastError, boolToInt(g.Enabled), boolToInt(g.ClientExpose), g.ClientAlias, g.UpdatedAt, g.ID,
	)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteGeoFile deletes a geo_file by id.
func DeleteGeoFile(ctx context.Context, q DBTX, id int64) error {
	res, err := q.ExecContext(ctx, `DELETE FROM geo_files WHERE id = ?`, id)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func scanGeoFile(s rowScanner) (*GeoFile, error) {
	var g GeoFile
	var sysOv, autoUpd, enabled, expose int
	err := s.Scan(
		&g.ID, &g.Name, &g.Title, &g.Kind, &sysOv, &g.SourceURL, &autoUpd,
		&g.SHA256, &g.SizeBytes, &g.CategoriesCount, &g.EntriesCount, &g.FetchedAt,
		&g.LastError, &enabled, &expose, &g.ClientAlias, &g.CreatedAt, &g.UpdatedAt,
	)
	if err != nil {
		return nil, mapErr(err)
	}
	g.IsSystemOverride = sysOv != 0
	g.AutoUpdate = autoUpd != 0
	g.Enabled = enabled != 0
	g.ClientExpose = expose != 0
	return &g, nil
}
