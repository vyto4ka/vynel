package usertransfer

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/vyto4ka/vynel/internal/panel/store"
)

// SourceXUI names 3x-ui in ExportMeta.Source.
const SourceXUI = "3x-ui"

// FetchXUI reads a 3x-ui database (x-ui.db, copied from the old server) into the transfer
// format. Both layouts are understood: clients embedded in each inbound's settings JSON (the
// classic layout) and the normalized clients/client_traffics tables (3x-ui v3).
//
// A 3x-ui "client" is one (email, uuid) entry of one inbound: the same person on two inbounds
// is two clients there. They merge here: one user, uuid of the first, traffic summed, the
// tightest expiry and limit win. The 3x-ui subscription token (subId) is not a vynel token;
// import mints a fresh link.
func FetchXUI(ctx context.Context, path string) (*ExportFile, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("cannot open %s: %w", path, err)
	}
	dsn := "file:" + path + "?mode=ro&immutable=1" // never writes the admin's copy
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	users, err := xuiUsers(ctx, db)
	if err != nil {
		return nil, err
	}
	return &ExportFile{Meta: ExportMeta{Format: Format, CreatedAt: unixNow(), Source: SourceXUI, Users: len(users)}, Users: users}, nil
}

// xuiClient is a 3x-ui client as both layouts see it. Times and limits of 0 mean "not set".
type xuiClient struct {
	Email      string
	UUID       string
	Enable     bool
	TotalGB    int64 // bytes; 0 = unlimited
	ExpiryTime int64 // ms since epoch, 3x-ui style; 0 = never
	LimitIP    int64
	TgID       int64
	Comment    string
	Reset      int64
	Up         int64
	Down       int64
}

func xuiUsers(ctx context.Context, db *sql.DB) ([]TransferUser, error) {
	clients, err := xuiClients(ctx, db)
	if err != nil {
		return nil, err
	}
	byEmail := map[string]*xuiClient{}
	order := []string{}
	for i := range clients {
		c := &clients[i]
		if c.Email == "" {
			continue
		}
		if first, ok := byEmail[c.Email]; ok {
			mergeXUI(first, c)
			continue
		}
		byEmail[c.Email] = c
		order = append(order, c.Email)
	}
	out := make([]TransferUser, 0, len(order))
	for _, email := range order {
		out = append(out, xuiToTransfer(byEmail[email]))
	}
	return out, nil
}

// mergeXUI folds the same person's second entry into the first: traffic sums, the tightest
// expiry and limit win, the enabled state of the last entry counts (3x-ui has no global switch).
func mergeXUI(a, b *xuiClient) {
	a.Up, a.Down = a.Up+b.Up, a.Down+b.Down
	if a.TotalGB == 0 || (b.TotalGB != 0 && b.TotalGB < a.TotalGB) {
		a.TotalGB = b.TotalGB
	}
	if a.ExpiryTime == 0 || (b.ExpiryTime != 0 && b.ExpiryTime < a.ExpiryTime) {
		a.ExpiryTime = b.ExpiryTime
	}
	if b.LimitIP != 0 {
		a.LimitIP = b.LimitIP
	}
	if b.TgID != 0 {
		a.TgID = b.TgID
	}
	if b.Comment != "" {
		a.Comment = b.Comment
	}
	a.Enable = b.Enable
}

func xuiToTransfer(c *xuiClient) TransferUser {
	t := TransferUser{
		Username:         c.Email,
		UUID:             c.UUID,
		Status:           store.StatusActive,
		TrafficUsedBytes: c.Up + c.Down,
		ResetStrategy:    xuiReset(c.Reset),
		Note:             c.Comment,
	}
	if !c.Enable {
		t.Status = store.StatusDisabled
	}
	if c.TotalGB > 0 {
		v := c.TotalGB
		t.TrafficLimitBytes = &v
	}
	if c.ExpiryTime > 0 {
		v := c.ExpiryTime / 1000 // 3x-ui keeps milliseconds
		t.ExpireAt = &v
	}
	if c.LimitIP > 0 {
		v := c.LimitIP
		t.IPLimit = &v
	}
	if c.TgID != 0 {
		v := c.TgID
		t.TelegramID = &v
	}
	return t
}

func xuiReset(r int64) string {
	switch r {
	case 1:
		return "day"
	case 2:
		return "week"
	case 3:
		return "month"
	}
	return "no"
}

// xuiClients reads the clients of both layouts: the normalized tables first (3x-ui v3), the
// settings JSON of every inbound as the fallback (the classic layout).
func xuiClients(ctx context.Context, db *sql.DB) ([]xuiClient, error) {
	rows, err := db.QueryContext(ctx, `SELECT email, uuid, enable, total_gb, expiry_time, limit_ip, tg_id, comment, reset FROM clients`)
	if err == nil {
		defer rows.Close()
		var out []xuiClient
		for rows.Next() {
			var c xuiClient
			var tg, reset sql.NullInt64
			var comment sql.NullString
			if err := rows.Scan(&c.Email, &c.UUID, &c.Enable, &c.TotalGB, &c.ExpiryTime, &c.LimitIP, &tg, &comment, &reset); err != nil {
				return nil, err
			}
			c.TgID, c.Comment, c.Reset = tg.Int64, comment.String, reset.Int64
			out = append(out, c)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		if len(out) > 0 {
			return xuiWithTraffic(ctx, db, out), nil
		}
	}
	// Classic layout: clients live in inbounds.settings JSON.
	ibRows, err := db.QueryContext(ctx, `SELECT settings FROM inbounds WHERE settings != ''`)
	if err != nil {
		return nil, fmt.Errorf("read inbounds: %w", err)
	}
	defer ibRows.Close()
	var out []xuiClient
	for ibRows.Next() {
		var settings string
		if err := ibRows.Scan(&settings); err != nil {
			return nil, err
		}
		var doc struct {
			Clients []struct {
				ID         string `json:"id"`
				Password   string `json:"password"`
				Email      string `json:"email"`
				LimitIP    int64  `json:"limitIp"`
				TotalGB    int64  `json:"totalGB"`
				ExpiryTime int64  `json:"expiryTime"`
				Enable     bool   `json:"enable"`
				TgID       any    `json:"tgId"`
				SubID      string `json:"subId"`
				Comment    string `json:"comment"`
				Reset      int64  `json:"reset"`
			} `json:"clients"`
		}
		if err := json.Unmarshal([]byte(settings), &doc); err != nil {
			continue // an inbound without clients or of another shape
		}
		for _, cl := range doc.Clients {
			c := xuiClient{Email: cl.Email, UUID: firstNonEmptyStr(cl.ID, cl.Password), Enable: cl.Enable,
				TotalGB: cl.TotalGB, ExpiryTime: cl.ExpiryTime, LimitIP: cl.LimitIP, Comment: cl.Comment, Reset: cl.Reset}
			c.TgID = tgOfAny(cl.TgID)
			out = append(out, c)
		}
	}
	if err := ibRows.Err(); err != nil {
		return nil, err
	}
	return xuiWithTraffic(ctx, db, out), nil
}

// xuiWithTraffic adds each client's counters from client_traffics (both layouts have it).
func xuiWithTraffic(ctx context.Context, db *sql.DB, clients []xuiClient) []xuiClient {
	rows, err := db.QueryContext(ctx, `SELECT email, up, down FROM client_traffics`)
	if err != nil {
		return clients // traffic is a nice-to-have, not a reason to fail
	}
	defer rows.Close()
	byEmail := map[string]*xuiClient{}
	for i := range clients {
		byEmail[clients[i].Email] = &clients[i]
	}
	for rows.Next() {
		var email string
		var up, down int64
		if err := rows.Scan(&email, &up, &down); err != nil {
			continue
		}
		if c, ok := byEmail[email]; ok {
			c.Up, c.Down = up, down
		}
	}
	return clients
}

// tgOfAny reads tgId, which 3x-ui has stored as number, string or empty at different times.
func tgOfAny(v any) int64 {
	switch t := v.(type) {
	case float64:
		return int64(t)
	case string:
		var n int64
		_, _ = fmt.Sscanf(strings.TrimSpace(t), "%d", &n)
		return n
	}
	return 0
}

func firstNonEmptyStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
