package store

import (
	"context"
	"database/sql"
)

// BaseConfig is the shared log/dns/outbounds/routing part of a node config.
type BaseConfig struct {
	ID        int64
	Name      string
	JSON      string
	IsDefault bool
}

// UpsertBaseConfig creates or updates a base config by name.
func UpsertBaseConfig(ctx context.Context, q DBTX, b *BaseConfig) error {
	now := unix()
	if b.IsDefault {
		if _, err := q.ExecContext(ctx, `UPDATE base_configs SET is_default=0`); err != nil {
			return err
		}
	}
	err := q.QueryRowContext(ctx, `INSERT INTO base_configs (name, json, is_default, created_at, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (name) DO UPDATE SET json=excluded.json, is_default=excluded.is_default, updated_at=excluded.updated_at
		RETURNING id`, b.Name, b.JSON, b.IsDefault, now, now).Scan(&b.ID)
	return mapErr(err)
}

// GetBaseConfig loads a base config; id nil returns the default one.
func GetBaseConfig(ctx context.Context, q DBTX, id *int64) (*BaseConfig, error) {
	b := &BaseConfig{}
	var row *sql.Row
	if id == nil {
		row = q.QueryRowContext(ctx, `SELECT id, name, json, is_default FROM base_configs WHERE is_default=1`)
	} else {
		row = q.QueryRowContext(ctx, `SELECT id, name, json, is_default FROM base_configs WHERE id=?`, *id)
	}
	if err := row.Scan(&b.ID, &b.Name, &b.JSON, &b.IsDefault); err != nil {
		return nil, mapErr(err)
	}
	return b, nil
}

// Profile is a shared inbound configuration created from a template.
type Profile struct {
	ID              int64
	Name            string
	TemplateID      string
	TemplateVersion int
	Values          map[string]any
	Override        map[string]any
	Inbound         map[string]any // own inbound source; nil = the template's
	Host            map[string]any // own connection point source; nil = the template's
	TagPattern      string
	RemarkPattern   string
	CreatedAt       int64
	UpdatedAt       int64
}

const profileCols = `id, name, template_id, template_version, values_json, override_json, tag_pattern, remark_pattern, created_at, updated_at,
	inbound_json, host_json`

func scanProfile(sc interface{ Scan(...any) error }) (*Profile, error) {
	p := &Profile{}
	var vals, over, inbound, host string
	if err := sc.Scan(&p.ID, &p.Name, &p.TemplateID, &p.TemplateVersion, &vals, &over, &p.TagPattern, &p.RemarkPattern, &p.CreatedAt, &p.UpdatedAt,
		&inbound, &host); err != nil {
		return nil, mapErr(err)
	}
	var err error
	if inbound != "" {
		if p.Inbound, err = fromJSONMap(inbound); err != nil {
			return nil, err
		}
	}
	if host != "" {
		if p.Host, err = fromJSONMap(host); err != nil {
			return nil, err
		}
	}
	if p.Values, err = fromJSONMap(vals); err != nil {
		return nil, err
	}
	if p.Override, err = fromJSONMap(over); err != nil {
		return nil, err
	}
	return p, nil
}

// CreateProfile inserts a profile.
func CreateProfile(ctx context.Context, q DBTX, p *Profile) error {
	now := unix()
	p.CreatedAt, p.UpdatedAt = now, now
	res, err := q.ExecContext(ctx, `INSERT INTO profiles (name, template_id, template_version, values_json, override_json, tag_pattern, remark_pattern, created_at, updated_at,
		inbound_json, host_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, p.Name, p.TemplateID, p.TemplateVersion, toJSON(p.Values), toJSON(p.Override),
		p.TagPattern, p.RemarkPattern, now, now, sourceJSON(p.Inbound), sourceJSON(p.Host))
	if err != nil {
		return mapErr(err)
	}
	p.ID, err = res.LastInsertId()
	return err
}

// UpdateProfile saves a profile.
func UpdateProfile(ctx context.Context, q DBTX, p *Profile) error {
	p.UpdatedAt = unix()
	return execOne(ctx, q, `UPDATE profiles SET name=?, values_json=?, override_json=?, tag_pattern=?, remark_pattern=?, updated_at=?,
		inbound_json=?, host_json=? WHERE id=?`,
		p.Name, toJSON(p.Values), toJSON(p.Override), p.TagPattern, p.RemarkPattern, p.UpdatedAt, sourceJSON(p.Inbound), sourceJSON(p.Host), p.ID)
}

// sourceJSON stores a profile source; nil (use the template's) is an empty string.
func sourceJSON(m map[string]any) string {
	if m == nil {
		return ""
	}
	return toJSON(m)
}

// GetProfile loads a profile.
func GetProfile(ctx context.Context, q DBTX, id int64) (*Profile, error) {
	return scanProfile(q.QueryRowContext(ctx, `SELECT `+profileCols+` FROM profiles WHERE id=?`, id))
}

// GetProfileByName loads a profile by name.
func GetProfileByName(ctx context.Context, q DBTX, name string) (*Profile, error) {
	return scanProfile(q.QueryRowContext(ctx, `SELECT `+profileCols+` FROM profiles WHERE name=?`, name))
}

// ListProfiles returns all profiles.
func ListProfiles(ctx context.Context, q DBTX) ([]*Profile, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+profileCols+` FROM profiles ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Profile
	for rows.Next() {
		p, err := scanProfile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// DeleteProfile removes a profile; it fails while node inbounds use it.
func DeleteProfile(ctx context.Context, q DBTX, id int64) error {
	return execOne(ctx, q, `DELETE FROM profiles WHERE id=?`, id)
}

// Address is an IP address of a node.
type Address struct {
	ID          int64
	NodeID      int64
	IP          string
	Family      string
	Interface   string
	OnInterface bool
	IsPrimary   bool
	Label       string
}

const addressCols = `id, node_id, ip, family, interface, on_interface, is_primary, label`

func scanAddress(sc interface{ Scan(...any) error }) (*Address, error) {
	a := &Address{}
	if err := sc.Scan(&a.ID, &a.NodeID, &a.IP, &a.Family, &a.Interface, &a.OnInterface, &a.IsPrimary, &a.Label); err != nil {
		return nil, mapErr(err)
	}
	return a, nil
}

// UpsertAddress inserts or updates an address by (node, ip). Label is only set on insert or when non-empty.
func UpsertAddress(ctx context.Context, q DBTX, a *Address) error {
	err := q.QueryRowContext(ctx, `INSERT INTO node_addresses (node_id, ip, family, interface, on_interface, is_primary, label)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (node_id, ip) DO UPDATE SET family=excluded.family, interface=excluded.interface,
			on_interface=excluded.on_interface, is_primary=excluded.is_primary,
			label=CASE WHEN excluded.label != '' THEN excluded.label ELSE node_addresses.label END
		RETURNING id`, a.NodeID, a.IP, a.Family, a.Interface, a.OnInterface, a.IsPrimary, a.Label).Scan(&a.ID)
	return mapErr(err)
}

// MarkAddressesMissing flags addresses of a node that the agent no longer reports.
func MarkAddressesMissing(ctx context.Context, q DBTX, nodeID int64, present []int64) error {
	if _, err := q.ExecContext(ctx, `UPDATE node_addresses SET on_interface=0, is_primary=0 WHERE node_id=?`, nodeID); err != nil {
		return err
	}
	for _, id := range present {
		if _, err := q.ExecContext(ctx, `UPDATE node_addresses SET on_interface=1 WHERE id=?`, id); err != nil {
			return err
		}
	}
	return nil
}

// GetAddress loads an address.
func GetAddress(ctx context.Context, q DBTX, id int64) (*Address, error) {
	return scanAddress(q.QueryRowContext(ctx, `SELECT `+addressCols+` FROM node_addresses WHERE id=?`, id))
}

// ListAddresses returns a node's addresses (nodeID 0 = all nodes).
func ListAddresses(ctx context.Context, q DBTX, nodeID int64) ([]*Address, error) {
	query, args := `SELECT `+addressCols+` FROM node_addresses ORDER BY node_id, is_primary DESC, id`, []any{}
	if nodeID != 0 {
		query, args = `SELECT `+addressCols+` FROM node_addresses WHERE node_id=? ORDER BY is_primary DESC, id`, []any{nodeID}
	}
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Address
	for rows.Next() {
		a, err := scanAddress(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// NodeInbound is a profile instance on a node ("its own VLESS profile", docs/INBOUNDS.md §1).
type NodeInbound struct {
	ID              int64
	NodeID          int64
	ProfileID       int64
	Tag             string
	ListenAddressID *int64
	EgressAddressID *int64
	PortOverride    int
	Values          map[string]any
	Override        map[string]any
	Enabled         bool
	Sort            int64
	CreatedAt       int64
	UpdatedAt       int64
	Host            map[string]any // overrides of the connection point (remark, address, port, sni, fingerprint, hidden)
}

const nodeInboundCols = `id, node_id, profile_id, tag, listen_address_id, egress_address_id, port_override, values_json, override_json, enabled, sort, created_at, updated_at, host_json`

func scanNodeInbound(sc interface{ Scan(...any) error }) (*NodeInbound, error) {
	ni := &NodeInbound{}
	var listen, egress sql.NullInt64
	var vals, over, host string
	if err := sc.Scan(&ni.ID, &ni.NodeID, &ni.ProfileID, &ni.Tag, &listen, &egress, &ni.PortOverride, &vals, &over, &ni.Enabled, &ni.Sort, &ni.CreatedAt, &ni.UpdatedAt, &host); err != nil {
		return nil, mapErr(err)
	}
	ni.ListenAddressID, ni.EgressAddressID = ptrInt(listen), ptrInt(egress)
	var err error
	if ni.Values, err = fromJSONMap(vals); err != nil {
		return nil, err
	}
	if ni.Override, err = fromJSONMap(over); err != nil {
		return nil, err
	}
	if ni.Host, err = fromJSONMap(host); err != nil {
		return nil, err
	}
	return ni, nil
}

// CreateNodeInbound inserts a node inbound.
func CreateNodeInbound(ctx context.Context, q DBTX, ni *NodeInbound) error {
	now := unix()
	ni.CreatedAt, ni.UpdatedAt = now, now
	res, err := q.ExecContext(ctx, `INSERT INTO node_inbounds (node_id, profile_id, tag, listen_address_id, egress_address_id, port_override, values_json, override_json, enabled, sort, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, ni.NodeID, ni.ProfileID, ni.Tag, nullInt(ni.ListenAddressID), nullInt(ni.EgressAddressID),
		ni.PortOverride, toJSON(ni.Values), toJSON(ni.Override), ni.Enabled, ni.Sort, now, now)
	if err != nil {
		return mapErr(err)
	}
	ni.ID, err = res.LastInsertId()
	return err
}

// UpdateNodeInbound saves a node inbound.
func UpdateNodeInbound(ctx context.Context, q DBTX, ni *NodeInbound) error {
	ni.UpdatedAt = unix()
	if ni.Host == nil {
		ni.Host = map[string]any{}
	}
	return execOne(ctx, q, `UPDATE node_inbounds SET tag=?, listen_address_id=?, egress_address_id=?, port_override=?, values_json=?, override_json=?, enabled=?, sort=?, updated_at=?, host_json=?
		WHERE id=?`, ni.Tag, nullInt(ni.ListenAddressID), nullInt(ni.EgressAddressID), ni.PortOverride, toJSON(ni.Values), toJSON(ni.Override),
		ni.Enabled, ni.Sort, ni.UpdatedAt, toJSON(ni.Host), ni.ID)
}

// GetNodeInbound loads a node inbound.
func GetNodeInbound(ctx context.Context, q DBTX, id int64) (*NodeInbound, error) {
	return scanNodeInbound(q.QueryRowContext(ctx, `SELECT `+nodeInboundCols+` FROM node_inbounds WHERE id=?`, id))
}

// GetNodeInboundByTag loads a node inbound by tag.
func GetNodeInboundByTag(ctx context.Context, q DBTX, tag string) (*NodeInbound, error) {
	return scanNodeInbound(q.QueryRowContext(ctx, `SELECT `+nodeInboundCols+` FROM node_inbounds WHERE tag=?`, tag))
}

// ListNodeInbounds returns inbounds filtered by node and/or profile (0 = any).
func ListNodeInbounds(ctx context.Context, q DBTX, nodeID, profileID int64) ([]*NodeInbound, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+nodeInboundCols+` FROM node_inbounds
		WHERE (?=0 OR node_id=?) AND (?=0 OR profile_id=?) ORDER BY node_id, sort, id`, nodeID, nodeID, profileID, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*NodeInbound
	for rows.Next() {
		ni, err := scanNodeInbound(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ni)
	}
	return out, rows.Err()
}

// TagExists reports whether an inbound tag is taken.
func TagExists(ctx context.Context, q DBTX, tag string) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM node_inbounds WHERE tag=?`, tag).Scan(&n)
	return n > 0, err
}

// DeleteNodeInbound removes a node inbound.
func DeleteNodeInbound(ctx context.Context, q DBTX, id int64) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM group_access WHERE kind='node_inbound' AND ref_id=?`, id); err != nil {
		return err
	}
	return execOne(ctx, q, `DELETE FROM node_inbounds WHERE id=?`, id)
}
