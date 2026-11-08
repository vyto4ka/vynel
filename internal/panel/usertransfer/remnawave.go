package usertransfer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// SourceRemnawave names Remnawave in ExportMeta.Source.
const SourceRemnawave = "remnawave"

// RemnawavePanel says where a Remnawave panel is and how to talk to it.
type RemnawavePanel struct {
	BaseURL string // e.g. https://panel.example.com
	Token   string // panel API token (Bearer)
}

// remnawaveFetchPrivate reaches private addresses too (CLI: the panel may sit in the admin's
// own network, or behind a Caddy on the same host). Tests set it to reach their servers.
var remnawaveFetchPrivate = false

// remnawaveClient fetches panels (tests replace it). Public addresses only unless
// remnawaveFetchPrivate: the panel address is typed by an admin, and the panel must not probe
// its own network (127.0.0.1, the cloud metadata service).
var remnawaveClient = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, _, _ := net.SplitHostPort(address)
			if ip := net.ParseIP(host); ip != nil && !remnawaveFetchPrivate &&
				(ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() ||
					ip.IsLinkLocalMulticast() || ip.IsMulticast()) {
				return nil, fmt.Errorf("address %s is not public", host)
			}
			var d net.Dialer
			return d.DialContext(ctx, network, address)
		},
		TLSHandshakeTimeout: 6 * time.Second,
	},
}

// rnUser is one user of the Remnawave answer (the fields we read of GetUsersCommand).
type rnUser struct {
	Username            string  `json:"username"`
	Status              string  `json:"status"`
	ShortUUID           string  `json:"shortUuid"`
	TrafficLimitBytes   int64   `json:"trafficLimitBytes"`
	UsedTrafficBytes    int64   `json:"usedTrafficBytes"`
	TrafficLimitStrat   string  `json:"trafficLimitStrategy"`
	ExpireAt            string  `json:"expireAt"`
	TelegramID          *int64  `json:"telegramId"`
	ExternalID          *string `json:"externalId"`
	Description         string  `json:"description"`
	HWIDDeviceLimit     *int64  `json:"hwidDeviceLimit"`
	VlessUUID           string  `json:"vlessUuid"`
	ActiveInternalSquad []struct {
		Name string `json:"name"`
	} `json:"activeInternalSquads"`
	UserTraffic struct {
		UsedTrafficBytes int64 `json:"usedTrafficBytes"`
	} `json:"userTraffic"`
}

// FetchRemnawave pulls every user of a Remnawave panel into the transfer format. Remnawave's
// shortUuid becomes SubToken so existing subscription links keep working when the subscription
// domain moves to vynel; if a user has no shortUuid, import mints a fresh token.
func FetchRemnawave(ctx context.Context, p RemnawavePanel) (*ExportFile, error) {
	base := strings.TrimSuffix(p.BaseURL, "/")
	if u, err := url.Parse(base); err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("bad panel address %q", p.BaseURL)
	}
	if p.Token == "" {
		return nil, errors.New("the Remnawave API token is empty")
	}
	var users []TransferUser
	total := -1
	for start := 0; total < 0 || start < total; start += rnPageSize {
		page, n, err := rnPage(ctx, base, p.Token, start)
		if err != nil {
			return nil, err
		}
		users = append(users, page...)
		if total < 0 {
			total = n
		}
		if len(page) == 0 {
			break // loop guard: a panel that ignores paging answers once
		}
	}
	f := &ExportFile{Meta: ExportMeta{Format: Format, CreatedAt: unixNow(), Source: SourceRemnawave, Users: len(users)}}
	f.Users = users
	return f, nil
}

const rnPageSize = 100

func rnPage(ctx context.Context, base, token string, start int) ([]TransferUser, int, error) {
	u := base + "/api/users?start=" + strconv.Itoa(start) + "&size=" + strconv.Itoa(rnPageSize)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := remnawaveClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("fetch %s: %w", u, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, 0, err
	}
	if resp.StatusCode != http.StatusOK {
		hint := ""
		if resp.StatusCode == http.StatusUnauthorized {
			hint = " (wrong token?)"
		}
		return nil, 0, fmt.Errorf("HTTP %d from %s%s", resp.StatusCode, u, hint)
	}
	var answer struct {
		Response struct {
			Users []rnUser `json:"users"`
			Total int      `json:"total"`
		} `json:"response"`
	}
	if err := json.Unmarshal(body, &answer); err != nil {
		return nil, 0, fmt.Errorf("cannot read the answer of %s: %w", u, err)
	}
	return convertRemnawaveUsers(answer.Response.Users), answer.Response.Total, nil
}

func convertRemnawaveUsers(raw []rnUser) []TransferUser {
	out := make([]TransferUser, 0, len(raw))
	for _, ru := range raw {
		used := ru.UserTraffic.UsedTrafficBytes
		if used == 0 {
			used = ru.UsedTrafficBytes
		}
		var tgID *int64
		if ru.TelegramID != nil && *ru.TelegramID > 0 {
			tgID = ru.TelegramID
		}
		var extID *string
		if ru.ExternalID != nil {
			if v := strings.TrimSpace(*ru.ExternalID); v != "" {
				extID = &v
			}
		}
		t := TransferUser{
			Username:          ru.Username,
			UUID:              ru.VlessUUID,
			SubToken:          strings.TrimSpace(ru.ShortUUID),
			Status:            rnStatus(ru.Status),
			TrafficLimitBytes: rnLimit(ru.TrafficLimitBytes),
			TrafficUsedBytes:  used,
			ResetStrategy:     rnReset(ru.TrafficLimitStrat),
			HWIDLimit:         ru.HWIDDeviceLimit,
			TelegramID:        tgID,
			ExternalID:        extID,
			Note:              ru.Description,
		}
		if ru.ExpireAt != "" {
			if ts, err := time.Parse(time.RFC3339, ru.ExpireAt); err == nil {
				v := ts.Unix()
				t.ExpireAt = &v
			}
		}
		for _, s := range ru.ActiveInternalSquad {
			if s.Name != "" {
				t.Groups = append(t.Groups, s.Name)
			}
		}
		out = append(out, t)
	}
	return out
}

// tryUnmarshalRemnawave detects a raw JSON dump of Remnawave's /api/users endpoint
// (either {"response":{"users":[...]}} or a raw [...] array of Remnawave user objects).
func tryUnmarshalRemnawave(b []byte) ([]TransferUser, bool) {
	var env struct {
		Response struct {
			Users []rnUser `json:"users"`
		} `json:"response"`
		Users []rnUser `json:"users"`
	}
	var list []rnUser
	if err := json.Unmarshal(b, &env); err == nil {
		if len(env.Response.Users) > 0 {
			list = env.Response.Users
		} else if len(env.Users) > 0 {
			list = env.Users
		}
	} else if err := json.Unmarshal(b, &list); err != nil {
		return nil, false
	}
	if len(list) == 0 {
		return nil, false
	}
	hasVless := false
	for _, u := range list {
		if u.VlessUUID != "" {
			hasVless = true
			break
		}
	}
	if !hasVless {
		return nil, false
	}
	return convertRemnawaveUsers(list), true
}

// rnStatus maps Remnawave's status; unknown values stay active (a name, not a verdict).
func rnStatus(s string) string {
	switch strings.ToUpper(s) {
	case "DISABLED":
		return "disabled"
	case "LIMITED":
		return "limited"
	case "EXPIRED":
		return "expired"
	}
	return "active"
}

func rnLimit(v int64) *int64 {
	if v <= 0 {
		return nil
	}
	return &v
}

func rnReset(s string) string {
	switch s {
	case "DAY":
		return "day"
	case "WEEK":
		return "week"
	case "MONTH", "MONTH_ROLLING":
		return "month"
	}
	return "no"
}

// RnSetPrivate lets tests reach test servers (and the CLI reach panels in its own network).
func RnSetPrivate(v bool) func() {
	remnawaveFetchPrivate = v
	return func() { remnawaveFetchPrivate = false }
}
