package subscription

import (
	"context"
	"encoding/base64"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
)

// Vars are the {variables} of header values and app links (service.SubVariables).
type Vars map[string]string

var statusText = map[string]string{
	store.StatusActive: "активна", store.StatusExpired: "истекла", store.StatusLimited: "трафик исчерпан", store.StatusDisabled: "отключена",
}

// UserVars computes the variables for a user. subURL is the user's subscription link.
func UserVars(u *store.User, subURL string, b service.SubBasics, now time.Time) Vars {
	var total, expire int64
	limit, expireDate, daysLeft := "∞", "бессрочно", ""
	if u.TrafficLimitBytes != nil {
		total = *u.TrafficLimitBytes
		limit = human(total)
	}
	if u.ExpireAt != nil {
		expire = *u.ExpireAt
		expireDate = time.Unix(expire, 0).Format("02.01.2006")
		d := (expire - now.Unix()) / 86400
		if d < 0 {
			d = 0
		}
		daysLeft = strconv.FormatInt(d, 10)
	}
	pageURL := b.PageURL
	if pageURL == "" {
		pageURL = subURL
	}
	hours := ""
	if b.UpdateHours > 0 {
		hours = strconv.Itoa(b.UpdateHours)
	}
	return Vars{
		"title": b.Title, "username": u.Username,
		"upload": "0", "download": strconv.FormatInt(u.TrafficUsedBytes, 10),
		"total": strconv.FormatInt(total, 10), "expire": strconv.FormatInt(expire, 10),
		"used": human(u.TrafficUsedBytes), "limit": limit, "expire_date": expireDate, "days_left": daysLeft,
		"status": statusText[u.Status], "update_hours": hours,
		"support_url": b.SupportURL, "announce": b.Announce, "announce_url": b.AnnounceURL,
		"sub_url": subURL, "page_url": pageURL, "url": subURL,
		"url_enc": url.QueryEscape(subURL), "url_b64": base64.StdEncoding.EncodeToString([]byte(subURL)),
		"title_url": url.PathEscape(b.Title),
	}
}

var varRe = regexp.MustCompile(`\{([a-z_]+)\}`)

// Expand replaces {name} with its value; unknown names stay as they are.
func (v Vars) Expand(s string) string {
	return varRe.ReplaceAllStringFunc(s, func(m string) string {
		if x, ok := v[m[1:len(m)-1]]; ok {
			return x
		}
		return m
	})
}

// HeaderValue is one rendered header.
type HeaderValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

var (
	reMu    sync.Mutex
	reCache = map[string]*regexp.Regexp{}
)

func clientRe(expr string) *regexp.Regexp {
	reMu.Lock()
	defer reMu.Unlock()
	re, ok := reCache[expr]
	if !ok {
		re, _ = regexp.Compile(expr) // validated on save; a broken one matches nobody
		if len(reCache) > 1000 {
			reCache = map[string]*regexp.Regexp{}
		}
		reCache[expr] = re
	}
	return re
}

// RenderHeaders returns the headers for a client. A header whose value renders empty, or that
// still contains an unfilled "…" placeholder, is skipped.
func RenderHeaders(hs []service.SubHeader, vars Vars, userAgent string) []HeaderValue {
	var out []HeaderValue
	for _, h := range hs {
		if !h.Enabled {
			continue
		}
		if h.Clients != "" {
			re := clientRe(h.Clients)
			if re == nil || !re.MatchString(userAgent) {
				continue
			}
		}
		v := strings.TrimSpace(vars.Expand(h.Value))
		if v == "" || strings.Contains(v, "…") {
			continue
		}
		if h.Base64 {
			v = "base64:" + base64.StdEncoding.EncodeToString([]byte(v))
		}
		out = append(out, HeaderValue{Name: h.Name, Value: v})
	}
	return out
}

// headersFor renders the configured headers for a request.
func (h *Handler) headersFor(ctx context.Context, u *store.User, userAgent string) []HeaderValue {
	subURL, err := h.svc.SubscriptionURL(ctx, u)
	if err != nil {
		subURL = ""
	}
	hs, err := h.svc.SubHeaders(ctx)
	if err != nil {
		h.log.Warn("subscription headers", "err", err)
	}
	return RenderHeaders(hs, UserVars(u, subURL, h.svc.SubBasicsGet(ctx), time.Now()), userAgent)
}
