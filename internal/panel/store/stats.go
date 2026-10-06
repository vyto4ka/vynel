package store

import (
	"context"
	"database/sql"
	"errors"
)

// Hour and Day truncate unix seconds (UTC).
func Hour(ts int64) int64 { return ts - ts%3600 }

// Day truncates unix seconds to the UTC day.
func Day(ts int64) int64 { return ts - ts%86400 }

// SetStatsCursor remembers the last ingested batch of a node.
func SetStatsCursor(ctx context.Context, q DBTX, nodeID int64, epoch string, seq int64) error {
	return execOne(ctx, q, `UPDATE nodes SET stats_epoch=?, stats_seq=? WHERE id=?`, epoch, seq, nodeID)
}

// AddUserTraffic adds a traffic delta to a user and to all per-user aggregates.
// It returns false when the user no longer exists.
func AddUserTraffic(ctx context.Context, q DBTX, userID, nodeID, ts, up, down int64) (bool, error) {
	res, err := q.ExecContext(ctx, `UPDATE users SET traffic_used_bytes = traffic_used_bytes + ?, lifetime_used_bytes = lifetime_used_bytes + ? WHERE id=?`,
		up+down, up+down, userID)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, nil
	}
	stmts := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO user_traffic_hourly (user_id, hour, up, down) VALUES (?, ?, ?, ?)
			ON CONFLICT (user_id, hour) DO UPDATE SET up = up + excluded.up, down = down + excluded.down`, []any{userID, Hour(ts), up, down}},
		{`INSERT INTO user_traffic_daily (user_id, day, up, down) VALUES (?, ?, ?, ?)
			ON CONFLICT (user_id, day) DO UPDATE SET up = up + excluded.up, down = down + excluded.down`, []any{userID, Day(ts), up, down}},
		{`INSERT INTO user_node_daily (user_id, node_id, day, up, down) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (user_id, node_id, day) DO UPDATE SET up = up + excluded.up, down = down + excluded.down`, []any{userID, nodeID, Day(ts), up, down}},
	}
	for _, st := range stmts {
		if _, err := q.ExecContext(ctx, st.sql, st.args...); err != nil {
			return false, err
		}
	}
	return true, nil
}

// AddNodeTraffic adds to a node's hourly traffic.
func AddNodeTraffic(ctx context.Context, q DBTX, nodeID, ts, up, down int64) error {
	_, err := q.ExecContext(ctx, `INSERT INTO node_traffic_hourly (node_id, hour, up, down) VALUES (?, ?, ?, ?)
		ON CONFLICT (node_id, hour) DO UPDATE SET up = up + excluded.up, down = down + excluded.down`, nodeID, Hour(ts), up, down)
	return err
}

// MarkOnline sets online_at for users seen online.
func MarkOnline(ctx context.Context, q DBTX, userID, ts int64) error {
	_, err := q.ExecContext(ctx, `UPDATE users SET online_at=? WHERE id=?`, ts, userID)
	return err
}

// Metrics is one node_metrics row.
type Metrics struct {
	NodeID   int64
	TS       int64
	CPU      float64
	MemUsed  int64
	MemTotal int64
	Load1    float64
	RxBps    int64
	TxBps    int64
	Uptime   int64
	Online   int64
}

// AddMetrics stores a metrics sample.
func AddMetrics(ctx context.Context, q DBTX, m Metrics) error {
	_, err := q.ExecContext(ctx, `INSERT OR REPLACE INTO node_metrics (node_id, ts, cpu, mem_used, mem_total, load1, rx_bps, tx_bps, uptime, online)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, m.NodeID, m.TS, m.CPU, m.MemUsed, m.MemTotal, m.Load1, m.RxBps, m.TxBps, m.Uptime, m.Online)
	return err
}

// LatestMetrics returns the newest sample of a node.
func LatestMetrics(ctx context.Context, q DBTX, nodeID int64) (*Metrics, error) {
	m := &Metrics{}
	err := q.QueryRowContext(ctx, `SELECT node_id, ts, cpu, mem_used, mem_total, load1, rx_bps, tx_bps, uptime, online
		FROM node_metrics WHERE node_id=? ORDER BY ts DESC LIMIT 1`, nodeID).
		Scan(&m.NodeID, &m.TS, &m.CPU, &m.MemUsed, &m.MemTotal, &m.Load1, &m.RxBps, &m.TxBps, &m.Uptime, &m.Online)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return m, err
}

// NodeTrafficSince sums a node's traffic since ts.
func NodeTrafficSince(ctx context.Context, q DBTX, nodeID, since int64) (up, down int64, err error) {
	err = q.QueryRowContext(ctx, `SELECT coalesce(sum(up),0), coalesce(sum(down),0) FROM node_traffic_hourly WHERE node_id=? AND hour >= ?`,
		nodeID, Hour(since)).Scan(&up, &down)
	return
}

// TrafficSince sums all users' traffic since ts (daily granularity).
func TrafficSince(ctx context.Context, q DBTX, since int64) (up, down int64, err error) {
	err = q.QueryRowContext(ctx, `SELECT coalesce(sum(up),0), coalesce(sum(down),0) FROM user_traffic_daily WHERE day >= ?`, Day(since)).Scan(&up, &down)
	return
}

// UserTotal is a user with a traffic sum.
type UserTotal struct {
	UserID   int64
	Username string
	Bytes    int64
}

// TopUsersSince returns users with the most traffic since ts.
func TopUsersSince(ctx context.Context, q DBTX, since int64, limit int) ([]UserTotal, error) {
	rows, err := q.QueryContext(ctx, `SELECT d.user_id, u.username, sum(d.up + d.down) AS b FROM user_traffic_daily d JOIN users u ON u.id = d.user_id
		WHERE d.day >= ? GROUP BY d.user_id ORDER BY b DESC LIMIT ?`, Day(since), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UserTotal
	for rows.Next() {
		var t UserTotal
		if err := rows.Scan(&t.UserID, &t.Username, &t.Bytes); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// NodeTotal is traffic of one user on one node.
type NodeTotal struct {
	NodeID int64
	Code   string
	Bytes  int64
}

// UserTrafficByNode sums a user's traffic per node since ts.
func UserTrafficByNode(ctx context.Context, q DBTX, userID, since int64) ([]NodeTotal, error) {
	rows, err := q.QueryContext(ctx, `SELECT d.node_id, coalesce(n.code, '#'||d.node_id), sum(d.up + d.down) FROM user_node_daily d LEFT JOIN nodes n ON n.id = d.node_id
		WHERE d.user_id=? AND d.day >= ? GROUP BY d.node_id ORDER BY 3 DESC`, userID, Day(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NodeTotal
	for rows.Next() {
		var t NodeTotal
		if err := rows.Scan(&t.NodeID, &t.Code, &t.Bytes); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// CountUsersByStatus returns status -> count.
func CountUsersByStatus(ctx context.Context, q DBTX) (map[string]int, error) {
	rows, err := q.QueryContext(ctx, `SELECT status, count(*) FROM users GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		out[st] = n
	}
	return out, rows.Err()
}

// CountOnlineSince counts users seen online since ts.
func CountOnlineSince(ctx context.Context, q DBTX, since int64) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE online_at >= ?`, since).Scan(&n)
	return n, err
}

// PruneStats deletes rows older than the retention windows (docs/ARCHITECTURE.md §9).
func PruneStats(ctx context.Context, q DBTX, now int64) error {
	stmts := []struct {
		sql string
		age int64
	}{
		{`DELETE FROM user_traffic_hourly WHERE hour < ?`, 7 * 86400},
		{`DELETE FROM user_node_daily WHERE day < ?`, 90 * 86400},
		{`DELETE FROM node_traffic_hourly WHERE hour < ?`, 90 * 86400},
		{`DELETE FROM node_metrics WHERE ts < ?`, 7 * 86400},
		{`DELETE FROM events_outbox WHERE ts < ?`, 30 * 86400},
	}
	for _, st := range stmts {
		if _, err := q.ExecContext(ctx, st.sql, now-st.age); err != nil {
			return err
		}
	}
	return nil
}

// UsersDueReset lists users whose reset strategy is not "no".
func UsersDueReset(ctx context.Context, q DBTX) ([]*User, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+userCols+` FROM users WHERE reset_strategy != 'no'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// DayTotal is the traffic of one day.
type DayTotal struct {
	Day      int64
	Up, Down int64
}

// TrafficByDay sums traffic per day since ts, for all users or one (userID > 0).
func TrafficByDay(ctx context.Context, q DBTX, userID, since int64) ([]DayTotal, error) {
	rows, err := q.QueryContext(ctx, `SELECT day, sum(up), sum(down) FROM user_traffic_daily WHERE day >= ? AND (? = 0 OR user_id = ?)
		GROUP BY day ORDER BY day`, Day(since), userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DayTotal
	for rows.Next() {
		var t DayTotal
		if err := rows.Scan(&t.Day, &t.Up, &t.Down); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
