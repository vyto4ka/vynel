package store

import "context"

// Device is a client device registered by HWID.
type Device struct {
	ID        int64
	UserID    int64
	HWID      string
	Platform  string
	OSVersion string
	Model     string
	UserAgent string
	FirstSeen int64
	LastSeen  int64
	LastIP    string
}

const deviceCols = `id, user_id, hwid, platform, os_version, model, user_agent, first_seen, last_seen, last_ip`

// ListDevices returns a user's devices, most recent first.
func ListDevices(ctx context.Context, q DBTX, userID int64) ([]*Device, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+deviceCols+` FROM user_devices WHERE user_id=? ORDER BY last_seen DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Device
	for rows.Next() {
		d := &Device{}
		if err := rows.Scan(&d.ID, &d.UserID, &d.HWID, &d.Platform, &d.OSVersion, &d.Model, &d.UserAgent, &d.FirstSeen, &d.LastSeen, &d.LastIP); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// TouchDevice updates a known device; it returns false if the device is not registered.
func TouchDevice(ctx context.Context, q DBTX, d *Device) (bool, error) {
	res, err := q.ExecContext(ctx, `UPDATE user_devices SET platform=?, os_version=?, model=?, user_agent=?, last_seen=?, last_ip=? WHERE user_id=? AND hwid=?`,
		d.Platform, d.OSVersion, d.Model, d.UserAgent, d.LastSeen, d.LastIP, d.UserID, d.HWID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// AddDevice registers a device.
func AddDevice(ctx context.Context, q DBTX, d *Device) error {
	res, err := q.ExecContext(ctx, `INSERT INTO user_devices (user_id, hwid, platform, os_version, model, user_agent, first_seen, last_seen, last_ip)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, d.UserID, d.HWID, d.Platform, d.OSVersion, d.Model, d.UserAgent, d.FirstSeen, d.LastSeen, d.LastIP)
	if err != nil {
		return mapErr(err)
	}
	d.ID, err = res.LastInsertId()
	return err
}

// CountDevices counts a user's devices.
func CountDevices(ctx context.Context, q DBTX, userID int64) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM user_devices WHERE user_id=?`, userID).Scan(&n)
	return n, err
}

// DeleteDevice removes a device of a user.
func DeleteDevice(ctx context.Context, q DBTX, userID, deviceID int64) error {
	return execOne(ctx, q, `DELETE FROM user_devices WHERE user_id=? AND id=?`, userID, deviceID)
}
