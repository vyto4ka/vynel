package store

import (
	"context"
	"encoding/json"
	"strings"
)

// NodeWarp stores per-node Cloudflare WARP (MASQUE / WireGuard) configuration and last check status.
type NodeWarp struct {
	NodeID         int64
	Enabled        bool
	Transport      string // "masque-h3" | "masque-h2" | "wireguard"
	RuleMode       string // "rules" | "all"
	CredMode       string // "auto" | "manual"
	DeviceID       string
	AccessToken    string
	LicenseKey     string
	AccountType    string
	SecretKey      string
	PublicKey      string
	PeerPublicKey  string
	PresharedKey   string
	AddrV4         string
	AddrV6         string
	Reserved       []int
	Endpoint       string
	MTU            int
	Keepalive      int
	DomainStrategy string
	Rules          []string
	LastCheckAt    int64
	LastCheckOK    bool
	LastCheckIP    string
	LastCheckColo  string
	LastCheckMs    int64
	LastCheckErr   string
	CreatedAt      int64
	UpdatedAt      int64
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// GetNodeWarp loads a node's WARP configuration, returning ErrNotFound when none exists.
func GetNodeWarp(ctx context.Context, q DBTX, nodeID int64) (*NodeWarp, error) {
	row := q.QueryRowContext(ctx, `
		SELECT node_id, enabled, transport, rule_mode, cred_mode, device_id, access_token, license_key,
		       account_type, secret_key, public_key, peer_public_key, preshared_key,
		       addr_v4, addr_v6, reserved_json, endpoint, mtu, keepalive, domain_strategy,
		       rules_json, last_check_at, last_check_ok, last_check_ip, last_check_colo,
		       last_check_ms, last_check_err, created_at, updated_at
		FROM node_warp WHERE node_id = ?`, nodeID)
	return scanNodeWarp(row)
}

// ListNodeWarps returns all stored node_warp rows indexed by node_id.
func ListNodeWarps(ctx context.Context, q DBTX) (map[int64]*NodeWarp, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT node_id, enabled, transport, rule_mode, cred_mode, device_id, access_token, license_key,
		       account_type, secret_key, public_key, peer_public_key, preshared_key,
		       addr_v4, addr_v6, reserved_json, endpoint, mtu, keepalive, domain_strategy,
		       rules_json, last_check_at, last_check_ok, last_check_ip, last_check_colo,
		       last_check_ms, last_check_err, created_at, updated_at
		FROM node_warp`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[int64]*NodeWarp{}
	for rows.Next() {
		w, err := scanNodeWarp(rows)
		if err != nil {
			return nil, err
		}
		out[w.NodeID] = w
	}
	return out, rows.Err()
}

// UpsertNodeWarp inserts or updates a node's WARP settings.
func UpsertNodeWarp(ctx context.Context, q DBTX, w *NodeWarp) error {
	now := unix()
	if w.CreatedAt == 0 {
		w.CreatedAt = now
	}
	w.UpdatedAt = now
	if w.Transport == "" {
		if strings.Contains(w.SecretKey, "PRIVATE KEY") || w.SecretKey == "" {
			w.Transport = "masque-h3"
		} else {
			w.Transport = "wireguard"
		}
	}
	if w.RuleMode == "" {
		w.RuleMode = "rules"
	}
	if w.CredMode == "" {
		w.CredMode = "auto"
	}
	if w.Endpoint == "" {
		if w.Transport == "masque-h3" || w.Transport == "masque-h2" {
			w.Endpoint = "162.159.198.2:443"
		} else {
			w.Endpoint = "engage.cloudflareclient.com:2408"
		}
	}
	if w.MTU <= 0 {
		w.MTU = 1280
	}
	if w.Keepalive < 0 {
		w.Keepalive = 25
	}
	if w.DomainStrategy == "" {
		w.DomainStrategy = "ForceIPv4v6"
	}
	resJSON := "[]"
	if len(w.Reserved) > 0 {
		b, _ := json.Marshal(w.Reserved)
		resJSON = string(b)
	}
	rulesJSON := "[]"
	if len(w.Rules) > 0 {
		b, _ := json.Marshal(w.Rules)
		rulesJSON = string(b)
	}
	_, err := q.ExecContext(ctx, `
		INSERT INTO node_warp (
			node_id, enabled, transport, rule_mode, cred_mode, device_id, access_token, license_key,
			account_type, secret_key, public_key, peer_public_key, preshared_key,
			addr_v4, addr_v6, reserved_json, endpoint, mtu, keepalive, domain_strategy,
			rules_json, last_check_at, last_check_ok, last_check_ip, last_check_colo,
			last_check_ms, last_check_err, created_at, updated_at
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(node_id) DO UPDATE SET
			enabled = excluded.enabled,
			transport = excluded.transport,
			rule_mode = excluded.rule_mode,
			cred_mode = excluded.cred_mode,
			device_id = excluded.device_id,
			access_token = excluded.access_token,
			license_key = excluded.license_key,
			account_type = excluded.account_type,
			secret_key = excluded.secret_key,
			public_key = excluded.public_key,
			peer_public_key = excluded.peer_public_key,
			preshared_key = excluded.preshared_key,
			addr_v4 = excluded.addr_v4,
			addr_v6 = excluded.addr_v6,
			reserved_json = excluded.reserved_json,
			endpoint = excluded.endpoint,
			mtu = excluded.mtu,
			keepalive = excluded.keepalive,
			domain_strategy = excluded.domain_strategy,
			rules_json = excluded.rules_json,
			last_check_at = excluded.last_check_at,
			last_check_ok = excluded.last_check_ok,
			last_check_ip = excluded.last_check_ip,
			last_check_colo = excluded.last_check_colo,
			last_check_ms = excluded.last_check_ms,
			last_check_err = excluded.last_check_err,
			updated_at = excluded.updated_at`,
		w.NodeID, boolToInt(w.Enabled), w.Transport, w.RuleMode, w.CredMode, w.DeviceID, w.AccessToken, w.LicenseKey,
		w.AccountType, w.SecretKey, w.PublicKey, w.PeerPublicKey, w.PresharedKey,
		w.AddrV4, w.AddrV6, resJSON, w.Endpoint, w.MTU, w.Keepalive, w.DomainStrategy,
		rulesJSON, w.LastCheckAt, boolToInt(w.LastCheckOK), w.LastCheckIP, w.LastCheckColo,
		w.LastCheckMs, w.LastCheckErr, w.CreatedAt, w.UpdatedAt,
	)
	return mapErr(err)
}

// UpdateNodeWarpCheck updates only the last live-check fields of node_warp.
func UpdateNodeWarpCheck(ctx context.Context, q DBTX, nodeID int64, ok bool, ip, colo string, rttMs int64, checkErr string) error {
	now := unix()
	_, err := q.ExecContext(ctx, `
		UPDATE node_warp
		SET last_check_at = ?, last_check_ok = ?, last_check_ip = ?, last_check_colo = ?,
		    last_check_ms = ?, last_check_err = ?, updated_at = ?
		WHERE node_id = ?`,
		now, boolToInt(ok), ip, colo, rttMs, checkErr, now, nodeID)
	return mapErr(err)
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanNodeWarp(s rowScanner) (*NodeWarp, error) {
	var w NodeWarp
	var enabled, checkOK int
	var resJSON, rulesJSON string
	err := s.Scan(
		&w.NodeID, &enabled, &w.Transport, &w.RuleMode, &w.CredMode, &w.DeviceID, &w.AccessToken, &w.LicenseKey,
		&w.AccountType, &w.SecretKey, &w.PublicKey, &w.PeerPublicKey, &w.PresharedKey,
		&w.AddrV4, &w.AddrV6, &resJSON, &w.Endpoint, &w.MTU, &w.Keepalive, &w.DomainStrategy,
		&rulesJSON, &w.LastCheckAt, &checkOK, &w.LastCheckIP, &w.LastCheckColo,
		&w.LastCheckMs, &w.LastCheckErr, &w.CreatedAt, &w.UpdatedAt,
	)
	if err != nil {
		return nil, mapErr(err)
	}
	w.Enabled = enabled != 0
	w.LastCheckOK = checkOK != 0
	_ = json.Unmarshal([]byte(resJSON), &w.Reserved)
	_ = json.Unmarshal([]byte(rulesJSON), &w.Rules)
	if w.Rules == nil {
		w.Rules = []string{}
	}
	return &w, nil
}
