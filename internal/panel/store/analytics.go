package store

import "context"

// Bucketed series for the statistics page. Buckets are aligned to UTC multiples of the bucket
// size (an hour or a day); missing buckets are filled in by the caller.

// TrafficPoint is traffic in one bucket.
type TrafficPoint struct {
	T        int64
	Up, Down int64
}

// UserTrafficSeries sums users' traffic (one user when userID > 0) per bucket in [from, to).
// Hourly buckets read the hourly table (kept 7 days), daily ones the daily table.
func UserTrafficSeries(ctx context.Context, q DBTX, from, to, bucket, userID int64) ([]TrafficPoint, error) {
	query := `SELECT (hour - hour % ?) AS t, sum(up), sum(down) FROM user_traffic_hourly
		WHERE hour >= ? AND hour < ? AND (? = 0 OR user_id = ?) GROUP BY t ORDER BY t`
	if bucket >= 86400 {
		query = `SELECT (day - day % ?) AS t, sum(up), sum(down) FROM user_traffic_daily
		WHERE day >= ? AND day < ? AND (? = 0 OR user_id = ?) GROUP BY t ORDER BY t`
	}
	rows, err := q.QueryContext(ctx, query, bucket, from, to, userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TrafficPoint
	for rows.Next() {
		var p TrafficPoint
		if err := rows.Scan(&p.T, &p.Up, &p.Down); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// NodePoint is one node's value in one bucket.
type NodePoint struct {
	NodeID int64
	T      int64
	Bytes  int64
}

// NodeTrafficSeries sums each node's traffic per bucket in [from, to) (kept 90 days).
func NodeTrafficSeries(ctx context.Context, q DBTX, from, to, bucket int64) ([]NodePoint, error) {
	rows, err := q.QueryContext(ctx, `SELECT node_id, (hour - hour % ?) AS t, sum(up + down) FROM node_traffic_hourly
		WHERE hour >= ? AND hour < ? GROUP BY node_id, t ORDER BY t`, bucket, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NodePoint
	for rows.Next() {
		var p NodePoint
		if err := rows.Scan(&p.NodeID, &p.T, &p.Bytes); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// MetricPoint is a node's averaged metrics in one bucket.
type MetricPoint struct {
	NodeID int64
	T      int64
	CPU    float64 // percent
	Mem    float64 // percent of total
	Online int64   // max users online in the bucket
	RxBps  int64   // average
	TxBps  int64
}

// MetricsSeries aggregates node metrics per bucket in [from, to) (kept 7 days).
func MetricsSeries(ctx context.Context, q DBTX, from, to, bucket int64) ([]MetricPoint, error) {
	rows, err := q.QueryContext(ctx, `SELECT node_id, (ts - ts % ?) AS t, avg(cpu),
		avg(CASE WHEN mem_total > 0 THEN 100.0 * mem_used / mem_total ELSE 0 END), max(online), CAST(avg(rx_bps) AS INTEGER), CAST(avg(tx_bps) AS INTEGER)
		FROM node_metrics WHERE ts >= ? AND ts < ? GROUP BY node_id, t ORDER BY t`, bucket, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MetricPoint
	for rows.Next() {
		var p MetricPoint
		if err := rows.Scan(&p.NodeID, &p.T, &p.CPU, &p.Mem, &p.Online, &p.RxBps, &p.TxBps); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ActiveUsers counts users with any traffic in [from, to) at daily granularity.
func ActiveUsers(ctx context.Context, q DBTX, from, to int64) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT count(DISTINCT user_id) FROM user_traffic_daily WHERE day >= ? AND day < ? AND up + down > 0`,
		Day(from), to).Scan(&n)
	return n, err
}

// TopUsersBetween returns users with the most traffic in [from, to).
func TopUsersBetween(ctx context.Context, q DBTX, from, to int64, limit int) ([]UserTotal, error) {
	rows, err := q.QueryContext(ctx, `SELECT d.user_id, u.username, sum(d.up + d.down) AS b FROM user_traffic_daily d JOIN users u ON u.id = d.user_id
		WHERE d.day >= ? AND d.day < ? GROUP BY d.user_id HAVING b > 0 ORDER BY b DESC LIMIT ?`, Day(from), to, limit)
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

// Count is a label with a count.
type Count struct {
	Label string
	N     int
}

// DevicePlatforms counts registered devices per platform.
func DevicePlatforms(ctx context.Context, q DBTX) ([]Count, error) {
	return counts(ctx, q, `SELECT platform, count(*) FROM user_devices GROUP BY platform ORDER BY 2 DESC`)
}

// SubscriptionAgents counts users by the User-Agent of their last subscription fetch.
func SubscriptionAgents(ctx context.Context, q DBTX) ([]Count, error) {
	return counts(ctx, q, `SELECT sub_last_ua, count(*) FROM users WHERE sub_last_ua != '' GROUP BY sub_last_ua`)
}

func counts(ctx context.Context, q DBTX, query string) ([]Count, error) {
	rows, err := q.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Count
	for rows.Next() {
		var c Count
		if err := rows.Scan(&c.Label, &c.N); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
