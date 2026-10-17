package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/store"
)

// DepthHeader marks a subscription request made by a panel merging another subscription in.
// A vynel panel answers it with its own servers only, without the subscriptions it merges in
// itself: two panels that include each other do not echo each other's servers back.
const DepthHeader = "X-Vynel-Depth"

// ExternalRefreshEvery is how often external subscriptions are fetched again; a failed fetch
// is retried sooner. Users always get the last good copy.
const (
	ExternalRefreshEvery = 30 * time.Minute
	externalRetry        = 5 * time.Minute
	externalMaxBody      = 2 << 20
)

// externalClient fetches external subscriptions (tests replace it).
var externalClient = &http.Client{Timeout: 15 * time.Second}

// RefreshExternalSub fetches an external subscription now and stores its servers (on failure
// the last good copy stays and the error is shown in the panel).
func (s *Service) RefreshExternalSub(ctx context.Context, id int64) error {
	e, err := store.GetExternalSub(ctx, s.st.DB, id)
	if err != nil {
		return err
	}
	links, ferr := s.fetchExternal(ctx, e.URL)
	msg := ""
	if ferr != nil {
		msg = ferr.Error()
	}
	if err := store.SetExternalFetch(ctx, s.st.DB, id, links, msg); err != nil {
		return err
	}
	s.Invalidate()
	if s.OnChange != nil {
		s.OnChange()
	}
	return ferr
}

var externalBusy sync.Mutex

// refreshDueExternals fetches the external subscriptions whose copy is old (in the background:
// a slow site must not hold up maintenance).
func (s *Service) refreshDueExternals(ctx context.Context) {
	if !externalBusy.TryLock() {
		return
	}
	subs, err := store.ListExternalSubs(ctx, s.st.DB)
	if err != nil {
		externalBusy.Unlock()
		return
	}
	now := s.now()
	var due []int64
	for _, e := range subs {
		if !e.Enabled {
			continue
		}
		every := ExternalRefreshEvery
		if e.Error != "" {
			every = externalRetry
		}
		if e.FetchedAt == nil && e.Error == "" || e.FetchedAt == nil && now.Sub(time.Unix(e.CreatedAt, 0)) > externalRetry ||
			e.FetchedAt != nil && now.Sub(time.Unix(*e.FetchedAt, 0)) >= every {
			due = append(due, e.ID)
		}
	}
	if len(due) == 0 {
		externalBusy.Unlock()
		return
	}
	go func() {
		defer externalBusy.Unlock()
		for _, id := range due {
			fctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			_ = s.RefreshExternalSub(fctx, id)
			cancel()
		}
	}()
}

// fetchExternal returns the links of a subscription, one per line. A link to a subscription of
// this panel is read directly (no HTTP, and its own merged subscriptions come along).
func (s *Service) fetchExternal(ctx context.Context, raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", errors.New("not a link")
	}
	if user := s.ownSubscriptionUser(ctx, u); user != nil {
		hosts, err := s.SubscriptionHosts(ctx, user, true)
		if err != nil {
			return "", err
		}
		var lines []string
		for _, h := range hosts {
			if !h.Hidden {
				lines = append(lines, HostLink(h, user.UUID))
			}
		}
		if user.Status != store.StatusActive || len(lines) == 0 {
			return "", fmt.Errorf("the subscription of %s on this panel has no servers (%s)", user.Username, user.Status)
		}
		return strings.Join(lines, "\n"), nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return "", err
	}
	// Ask for links (the format every panel has), as a regular app with a steady device id.
	sum := sha256.Sum256([]byte("vynel-external|" + raw))
	req.Header.Set("User-Agent", "v2rayNG/1.10.0")
	req.Header.Set("x-hwid", hex.EncodeToString(sum[:8]))
	req.Header.Set("x-device-os", "Linux")
	req.Header.Set("x-device-model", "vynel")
	req.Header.Set(DepthHeader, "1")
	resp, err := externalClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, externalMaxBody))
	if err != nil {
		return "", err
	}
	lines := linkLines(body)
	if len(lines) == 0 {
		return "", errors.New("no server links in the answer (only links in base64 or plain text are understood)")
	}
	return strings.Join(lines, "\n"), nil
}

// ownSubscriptionUser is the user whose subscription a link of this panel is (nil = not ours).
func (s *Service) ownSubscriptionUser(ctx context.Context, u *url.URL) *store.User {
	domain, _ := s.Setting(ctx, SettingSubDomain, "")
	if domain == "" || !strings.EqualFold(u.Hostname(), domain) {
		return nil
	}
	token, ok := strings.CutPrefix(u.Path, s.SubPrefix(ctx))
	if !ok {
		return nil
	}
	token, _, _ = strings.Cut(token, "/")
	user, err := store.GetUserBySubToken(ctx, s.st.DB, token)
	if err != nil {
		return nil
	}
	return user
}

// linkLines finds the server links in a subscription body: base64 (any flavour) or plain text.
func linkLines(body []byte) []string {
	text := strings.TrimSpace(string(body))
	if !strings.Contains(text, "://") {
		compact := strings.Map(func(r rune) rune {
			if r == '\n' || r == '\r' || r == ' ' {
				return -1
			}
			return r
		}, text)
		for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			if b, err := enc.DecodeString(compact); err == nil && strings.Contains(string(b), "://") {
				text = string(b)
				break
			}
		}
	}
	var out []string
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if i := strings.Index(l, "://"); i > 0 && !strings.HasPrefix(l, "http") && !strings.ContainsAny(l[:i], " \t") {
			out = append(out, l)
		}
	}
	return out
}

// ParseLinks turns stored links into hosts. VLESS (raw/xhttp) and Hysteria2 links become full
// hosts every format can carry; other links go on as they are, in the links format only.
func ParseLinks(text string) []Host {
	var out []Host
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if h, ok := parseLink(l); ok {
			out = append(out, h)
			continue
		}
		remark := l
		if i := strings.LastIndex(l, "#"); i >= 0 {
			if r, err := url.PathUnescape(l[i+1:]); err == nil {
				remark = r
			}
		}
		out = append(out, Host{Raw: l, Remark: remark, Protocol: "raw"})
	}
	return out
}

func parseLink(l string) (Host, bool) {
	u, err := url.Parse(l)
	if err != nil || u.User == nil || u.Hostname() == "" {
		return Host{}, false
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port <= 0 {
		return Host{}, false
	}
	remark, _ := url.PathUnescape(u.Fragment)
	q := u.Query()
	h := Host{Remark: remark, Address: u.Hostname(), Port: port, UUID: u.User.Username(), SNI: q.Get("sni")}
	if a := q.Get("alpn"); a != "" {
		h.ALPN = strings.Split(a, ",")
	}
	switch u.Scheme {
	case "hysteria2", "hy2":
		if q.Get("obfs") != "" || q.Get("insecure") == "1" || q.Get("pinSHA256") != "" {
			return Host{}, false // options our formats do not carry
		}
		if _, ok := u.User.Password(); ok {
			return Host{}, false // user:pass auth
		}
		h.Protocol, h.Network, h.Security = "hysteria", "hysteria", "tls"
		if h.SNI == "" && net.ParseIP(h.Address) == nil {
			h.SNI = h.Address
		}
		return h, true
	case "vless":
		h.Protocol, h.Security, h.Flow, h.Fingerprint = "vless", q.Get("security"), q.Get("flow"), q.Get("fp")
		if h.Security == "" {
			h.Security = "none"
		}
		if e := q.Get("encryption"); e != "" && e != "none" {
			return Host{}, false
		}
		switch q.Get("type") {
		case "", "tcp", "raw":
			h.Network = "tcp"
			if q.Get("headerType") != "" && q.Get("headerType") != "none" {
				return Host{}, false
			}
		case "xhttp":
			h.Network, h.Path, h.HostHeader, h.Mode = "xhttp", q.Get("path"), q.Get("host"), q.Get("mode")
			if x := q.Get("extra"); x != "" {
				if json.Unmarshal([]byte(x), &h.Extra) != nil {
					return Host{}, false
				}
			}
		default:
			return Host{}, false // ws, grpc…: passed on as links
		}
		switch h.Security {
		case "reality":
			h.PublicKey, h.ShortID = q.Get("pbk"), q.Get("sid")
			if h.PublicKey == "" {
				return Host{}, false
			}
		case "tls", "none":
		default:
			return Host{}, false
		}
		return h, true
	}
	return Host{}, false
}

// SubscriptionHosts is everything a user's subscription lists: the own servers, then cascades
// and servers from merged subscriptions (withExternal), each server once.
func (s *Service) SubscriptionHosts(ctx context.Context, u *store.User, withExternal bool) ([]Host, error) {
	groups, err := store.UserGroupIDs(ctx, s.st.DB, u.ID)
	if err != nil {
		return nil, err
	}
	snap, err := s.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	own := snap.userHosts(groups)
	return append(own, s.extraHosts(snap, u, groups, own, withExternal)...), nil
}
