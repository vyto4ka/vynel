package store

import (
	"context"
	"database/sql"
	"encoding/json"
)

// Node is a server running the agent.
type Node struct {
	ID              int64
	Name            string
	Code            string
	Country         string
	Domain          string
	BaseConfigID    *int64
	Tags            []string
	Local           bool
	Enabled         bool
	CertSerial      string
	AgentVersion    string
	XrayVersion     string
	LastSeenAt      *int64
	DesiredRevision int64
	DesiredHash     string
	AppliedHash     string
	LastError       string
	Sort            int64
	CreatedAt       int64
	UpdatedAt       int64
	StatsEpoch      string
	StatsSeq        int64
	Warnings        []string
	CaddyVersion    string
}

const nodeCols = `id, name, code, country, domain, base_config_id, tags, local, enabled, cert_serial,
	agent_version, xray_version, last_seen_at, desired_revision, desired_hash, applied_hash, last_error, sort,
	created_at, updated_at, stats_epoch, stats_seq, warnings, caddy_version`

func scanNode(sc interface{ Scan(...any) error }) (*Node, error) {
	n := &Node{}
	var base, seen sql.NullInt64
	var tags, warnings string
	err := sc.Scan(&n.ID, &n.Name, &n.Code, &n.Country, &n.Domain, &base, &tags, &n.Local, &n.Enabled, &n.CertSerial,
		&n.AgentVersion, &n.XrayVersion, &seen, &n.DesiredRevision, &n.DesiredHash, &n.AppliedHash, &n.LastError, &n.Sort,
		&n.CreatedAt, &n.UpdatedAt, &n.StatsEpoch, &n.StatsSeq, &warnings, &n.CaddyVersion)
	if err != nil {
		return nil, mapErr(err)
	}
	n.BaseConfigID, n.LastSeenAt = ptrInt(base), ptrInt(seen)
	_ = json.Unmarshal([]byte(tags), &n.Tags)
	_ = json.Unmarshal([]byte(warnings), &n.Warnings)
	return n, nil
}

// CreateNode inserts a node and sets n.ID.
func CreateNode(ctx context.Context, q DBTX, n *Node) error {
	now := unix()
	n.CreatedAt, n.UpdatedAt = now, now
	if n.Tags == nil {
		n.Tags = []string{}
	}
	res, err := q.ExecContext(ctx, `INSERT INTO nodes (name, code, country, domain, base_config_id, tags, local, enabled, sort, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		n.Name, n.Code, n.Country, n.Domain, nullInt(n.BaseConfigID), toJSON(n.Tags), n.Local, n.Enabled, n.Sort, now, now)
	if err != nil {
		return mapErr(err)
	}
	n.ID, err = res.LastInsertId()
	return err
}

// UpdateNode saves the editable fields.
func UpdateNode(ctx context.Context, q DBTX, n *Node) error {
	n.UpdatedAt = unix()
	if n.Tags == nil {
		n.Tags = []string{}
	}
	return execOne(ctx, q, `UPDATE nodes SET name=?, code=?, country=?, domain=?, base_config_id=?, tags=?, enabled=?, sort=?, updated_at=?
		WHERE id=?`, n.Name, n.Code, n.Country, n.Domain, nullInt(n.BaseConfigID), toJSON(n.Tags), n.Enabled, n.Sort, n.UpdatedAt, n.ID)
}

// GetNode loads a node by id.
func GetNode(ctx context.Context, q DBTX, id int64) (*Node, error) {
	return scanNode(q.QueryRowContext(ctx, `SELECT `+nodeCols+` FROM nodes WHERE id=?`, id))
}

// GetNodeByCode loads a node by its code.
func GetNodeByCode(ctx context.Context, q DBTX, code string) (*Node, error) {
	return scanNode(q.QueryRowContext(ctx, `SELECT `+nodeCols+` FROM nodes WHERE code=?`, code))
}

// GetNodeByCertSerial finds the node owning a client certificate.
func GetNodeByCertSerial(ctx context.Context, q DBTX, serial string) (*Node, error) {
	if serial == "" {
		return nil, ErrNotFound
	}
	return scanNode(q.QueryRowContext(ctx, `SELECT `+nodeCols+` FROM nodes WHERE cert_serial=?`, serial))
}

// ListNodes returns all nodes ordered for display.
func ListNodes(ctx context.Context, q DBTX) ([]*Node, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+nodeCols+` FROM nodes ORDER BY sort, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// DeleteNode removes a node and (by cascade) its inbounds, addresses and tokens.
func DeleteNode(ctx context.Context, q DBTX, id int64) error {
	return execOne(ctx, q, `DELETE FROM nodes WHERE id=?`, id)
}

// SetNodeCert stores the serial of the node's current client certificate (” revokes).
func SetNodeCert(ctx context.Context, q DBTX, id int64, serial string) error {
	return execOne(ctx, q, `UPDATE nodes SET cert_serial=?, updated_at=? WHERE id=?`, serial, unix(), id)
}

// SetNodeDesired records the latest desired state hash and revision.
func SetNodeDesired(ctx context.Context, q DBTX, id, revision int64, hash string) error {
	return execOne(ctx, q, `UPDATE nodes SET desired_revision=?, desired_hash=? WHERE id=?`, revision, hash, id)
}

// NodeRuntime is what the gateway learns from a connected agent.
type NodeRuntime struct {
	AgentVersion string
	XrayVersion  string
	AppliedHash  string
	LastError    string
}

// SetNodeRuntime updates live fields reported by the agent.
func SetNodeRuntime(ctx context.Context, q DBTX, id int64, rt NodeRuntime) error {
	return execOne(ctx, q, `UPDATE nodes SET agent_version=?, xray_version=?, applied_hash=?, last_error=?, last_seen_at=? WHERE id=?`,
		rt.AgentVersion, rt.XrayVersion, rt.AppliedHash, rt.LastError, unix(), id)
}

// SetNodeFacts stores host facts reported in Hello.
func SetNodeFacts(ctx context.Context, q DBTX, id int64, caddyVersion string, warnings []string) error {
	if warnings == nil {
		warnings = []string{}
	}
	return execOne(ctx, q, `UPDATE nodes SET caddy_version=?, warnings=? WHERE id=?`, caddyVersion, toJSON(warnings), id)
}

// TouchNode updates last_seen_at.
func TouchNode(ctx context.Context, q DBTX, id int64) error {
	return execOne(ctx, q, `UPDATE nodes SET last_seen_at=? WHERE id=?`, unix(), id)
}

func execOne(ctx context.Context, q DBTX, query string, args ...any) error {
	res, err := q.ExecContext(ctx, query, args...)
	if err != nil {
		return mapErr(err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// InstallToken is a one-time node registration secret (stored hashed).
type InstallToken struct {
	ID        int64
	NodeID    int64
	TokenHash string
	ExpiresAt int64
	UsedAt    *int64
}

// CreateInstallToken stores a new token hash and invalidates older unused tokens of the node.
func CreateInstallToken(ctx context.Context, q DBTX, nodeID int64, hash string, expiresAt int64) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM node_install_tokens WHERE node_id=? AND used_at IS NULL`, nodeID); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, `INSERT INTO node_install_tokens (node_id, token_hash, expires_at, created_at) VALUES (?, ?, ?, ?)`,
		nodeID, hash, expiresAt, unix())
	return mapErr(err)
}

// GetInstallToken finds a token by hash.
func GetInstallToken(ctx context.Context, q DBTX, hash string) (*InstallToken, error) {
	t := &InstallToken{}
	var used sql.NullInt64
	err := q.QueryRowContext(ctx, `SELECT id, node_id, token_hash, expires_at, used_at FROM node_install_tokens WHERE token_hash=?`, hash).
		Scan(&t.ID, &t.NodeID, &t.TokenHash, &t.ExpiresAt, &used)
	if err != nil {
		return nil, mapErr(err)
	}
	t.UsedAt = ptrInt(used)
	return t, nil
}

// MarkInstallTokenUsed consumes a token; it fails if the token was already used.
func MarkInstallTokenUsed(ctx context.Context, q DBTX, id int64) error {
	return execOne(ctx, q, `UPDATE node_install_tokens SET used_at=? WHERE id=? AND used_at IS NULL`, unix(), id)
}
