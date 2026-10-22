package store

import (
	"context"
	"strings"
)

// Queries of the «Аналитика» page: traffic by where it went, every device, every address.

// RecordIP notes that a user was online from ip on a node at ts.
func RecordIP(ctx context.Context, q DBTX, userID int64, ip string, nodeID, ts int64) error {
	_, err := q.ExecContext(ctx, `INSERT INTO user_ips (user_id, ip, node_id, first_at, last_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (user_id, ip) DO UPDATE SET node_id = excluded.node_id, last_at = max(last_at, excluded.last_at)`,
		userID, ip, nodeID, ts, ts)
	return err
}

// RefTraffic is traffic of a ref ("i<inbound>", "c<cascade>", "") on a day or of a user.
type RefTraffic struct {
	Day      int64 // RefTrafficByDay
	UserID   int64 // RefTrafficByUser
	Username string
	Ref      string
	Up, Down int64
}

// RefTrafficByDay sums the traffic of each ref per day in [from, to), of one user when userID > 0.
func RefTrafficByDay(ctx context.Context, q DBTX, from, to, userID int64) ([]RefTraffic, error) {
	rows, err := q.QueryContext(ctx, `SELECT day, ref, sum(up), sum(down) FROM user_ref_daily
		WHERE day >= ? AND day < ? AND (? = 0 OR user_id = ?) GROUP BY day, ref ORDER BY day`, from, to, userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RefTraffic
	for rows.Next() {
		var r RefTraffic
		if err := rows.Scan(&r.Day, &r.Ref, &r.Up, &r.Down); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RefTrafficByUser sums each user's traffic per ref in [from, to), of one user when userID > 0.
func RefTrafficByUser(ctx context.Context, q DBTX, from, to, userID int64) ([]RefTraffic, error) {
	rows, err := q.QueryContext(ctx, `SELECT d.user_id, u.username, d.ref, sum(d.up), sum(d.down) FROM user_ref_daily d
		JOIN users u ON u.id = d.user_id WHERE d.day >= ? AND d.day < ? AND (? = 0 OR d.user_id = ?)
		GROUP BY d.user_id, d.ref`, from, to, userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RefTraffic
	for rows.Next() {
		var r RefTraffic
		if err := rows.Scan(&r.UserID, &r.Username, &r.Ref, &r.Up, &r.Down); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UserDevice is a device with its user's name.
type UserDevice struct {
	Device
	Username string
}

// like turns a search into a LIKE pattern ("" matches everything).
func like(s string) string {
	s = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.TrimSpace(s))
	return "%" + s + "%"
}

// AllDevices lists every user's devices, most recent first, matching a search over the user,
// HWID, model, platform and address; total is the count of matches.
func AllDevices(ctx context.Context, q DBTX, search string, limit, offset int) (out []*UserDevice, total int, err error) {
	const where = ` FROM user_devices d JOIN users u ON u.id = d.user_id
		WHERE u.username LIKE ?1 ESCAPE '\' OR d.hwid LIKE ?1 ESCAPE '\' OR d.model LIKE ?1 ESCAPE '\'
		OR d.platform LIKE ?1 ESCAPE '\' OR d.last_ip LIKE ?1 ESCAPE '\' OR d.user_agent LIKE ?1 ESCAPE '\'`
	pat := like(search)
	if err = q.QueryRowContext(ctx, `SELECT count(*)`+where, pat).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := q.QueryContext(ctx, `SELECT d.id, d.user_id, d.hwid, d.platform, d.os_version, d.model, d.user_agent,
		d.first_seen, d.last_seen, d.last_ip, u.username`+where+` ORDER BY d.last_seen DESC LIMIT ?2 OFFSET ?3`, pat, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		d := &UserDevice{}
		if err := rows.Scan(&d.ID, &d.UserID, &d.HWID, &d.Platform, &d.OSVersion, &d.Model, &d.UserAgent,
			&d.FirstSeen, &d.LastSeen, &d.LastIP, &d.Username); err != nil {
			return nil, 0, err
		}
		out = append(out, d)
	}
	return out, total, rows.Err()
}

// UserIP is an address a user was online from.
type UserIP struct {
	UserID   int64
	Username string
	IP       string
	NodeID   int64
	FirstAt  int64
	LastAt   int64
}

// AllIPs lists the addresses users were online from (kept 90 days), most recent first, matching
// a search over the user and the address; total is the count of matches.
func AllIPs(ctx context.Context, q DBTX, search string, limit, offset int) (out []*UserIP, total int, err error) {
	const where = ` FROM user_ips i JOIN users u ON u.id = i.user_id WHERE u.username LIKE ?1 ESCAPE '\' OR i.ip LIKE ?1 ESCAPE '\'`
	pat := like(search)
	if err = q.QueryRowContext(ctx, `SELECT count(*)`+where, pat).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := q.QueryContext(ctx, `SELECT i.user_id, u.username, i.ip, i.node_id, i.first_at, i.last_at`+where+
		` ORDER BY i.last_at DESC LIMIT ?2 OFFSET ?3`, pat, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		r := &UserIP{}
		if err := rows.Scan(&r.UserID, &r.Username, &r.IP, &r.NodeID, &r.FirstAt, &r.LastAt); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

// DayCount is a number on a day.
type DayCount struct {
	Day int64
	N   int64
}

// NewDevicesByDay counts devices first seen on each day in [from, to).
func NewDevicesByDay(ctx context.Context, q DBTX, from, to int64) ([]DayCount, error) {
	return dayCounts(ctx, q, `SELECT first_seen - first_seen % 86400 AS d, count(*) FROM user_devices
		WHERE first_seen >= ? AND first_seen < ? GROUP BY d ORDER BY d`, from, to)
}

// NewIPsByDay counts the addresses users came from for the first time on each day in [from, to).
func NewIPsByDay(ctx context.Context, q DBTX, from, to int64) ([]DayCount, error) {
	return dayCounts(ctx, q, `SELECT first_at - first_at % 86400 AS d, count(*) FROM user_ips
		WHERE first_at >= ? AND first_at < ? GROUP BY d ORDER BY d`, from, to)
}

func dayCounts(ctx context.Context, q DBTX, query string, from, to int64) ([]DayCount, error) {
	rows, err := q.QueryContext(ctx, query, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DayCount
	for rows.Next() {
		var c DayCount
		if err := rows.Scan(&c.Day, &c.N); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
