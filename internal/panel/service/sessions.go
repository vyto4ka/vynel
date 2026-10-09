package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/store"
)

// SettingWebPasswordLogin = "false" turns the login form off: only the Telegram bot lets in
// (docs/STEALTH.md §1).
const SettingWebPasswordLogin = "web.password_login"

// Login methods.
const (
	LoginPassword = "password"
	LoginLink     = "link"     // one-time link from the bot or `vynel admin login-link`
	LoginTelegram = "telegram" // Telegram Mini App
)

// Notice is something the bot tells the admins about right away.
type Notice struct {
	Kind    string // login
	Session *store.WebSession
}

// notices is buffered: a slow or stopped bot never blocks a login.
func (s *Service) noticeCh() chan Notice {
	s.noticeOnce.Do(func() { s.notices = make(chan Notice, 32) })
	return s.notices
}

// Notices delivers notices to the bot.
func (s *Service) Notices() <-chan Notice { return s.noticeCh() }

func (s *Service) notify(n Notice) {
	select {
	case s.noticeCh() <- n:
	default:
	}
}

// StartWebSession records a sign-in and tells the bot about it.
func (s *Service) StartWebSession(ctx context.Context, method, actor, ip, userAgent string, ttl time.Duration) (*store.WebSession, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	now := s.now().Unix()
	if len(userAgent) > 300 {
		userAgent = userAgent[:300]
	}
	w := &store.WebSession{ID: hex.EncodeToString(b), Method: method, Actor: actor, IP: ip, UserAgent: userAgent,
		CreatedAt: now, LastSeenAt: now, ExpiresAt: now + int64(ttl/time.Second)}
	err := s.mutate(ctx, change{actor: Actor{Kind: "web", ID: actor}, action: "web.login", entity: "web",
		diff: map[string]string{"method": method, "ip": ip}},
		func(q store.DBTX) error {
			_ = store.PruneWebSessions(ctx, q, now-30*86400)
			return store.CreateWebSession(ctx, q, w)
		})
	if err != nil {
		return nil, err
	}
	s.notify(Notice{Kind: "login", Session: w})
	return w, nil
}

// WebSessionActive reports whether a session may be used; it records activity at most once a
// minute.
func (s *Service) WebSessionActive(ctx context.Context, id, ip string) bool {
	w, err := store.GetWebSession(ctx, s.st.DB, id)
	now := s.now().Unix()
	if err != nil || w.RevokedAt != nil || w.ExpiresAt <= now {
		return false
	}
	if now-w.LastSeenAt >= 60 || (ip != "" && ip != w.IP) {
		_ = store.TouchWebSession(ctx, s.st.DB, id, now, ip)
	}
	return true
}

// WebSessions lists active sessions.
func (s *Service) WebSessions(ctx context.Context) ([]*store.WebSession, error) {
	return store.ListWebSessions(ctx, s.st.DB, s.now().Unix())
}

// EndWebSessions ends one session, or all but keep when id is empty. It returns how many ended.
func (s *Service) EndWebSessions(ctx context.Context, actor Actor, id, keep string) (int64, error) {
	var n int64
	err := s.mutate(ctx, change{actor: actor, action: "web.logout", entity: "web", diff: map[string]string{"session": id, "keep": keep}},
		func(q store.DBTX) error {
			var err error
			n, err = store.RevokeWebSessions(ctx, q, id, keep, s.now().Unix())
			return err
		})
	return n, err
}

// PasswordLoginEnabled reports whether the login form works.
func (s *Service) PasswordLoginEnabled(ctx context.Context) bool {
	v, _ := s.Setting(ctx, SettingWebPasswordLogin, "true")
	return v != "false"
}

// SetPasswordLogin turns the login form on or off. Off needs a working way in: a bot token and
// at least one bound admin.
func (s *Service) SetPasswordLogin(ctx context.Context, actor Actor, on bool) error {
	if !on {
		admins, err := s.BotAdmins(ctx)
		if err != nil {
			return err
		}
		if s.BotToken(ctx) == "" || len(admins) == 0 {
			return invalid("сначала подключите Telegram-бота и привяжите себя: без пароля в панель можно будет войти только через него")
		}
	}
	return s.SetSetting(ctx, actor, SettingWebPasswordLogin, strconv.FormatBool(on))
}

// TelegramAppLogin checks Telegram Mini App init data (core.telegram.org/bots/webapps
// #validating-data-received-via-the-mini-app) and returns the Telegram id of the bound admin
// who opened the panel. The data is signed with the bot token and lives five minutes.
func (s *Service) TelegramAppLogin(ctx context.Context, initData string) (int64, error) {
	token := s.BotToken(ctx)
	if token == "" {
		return 0, invalid("the bot is not configured")
	}
	vals, err := url.ParseQuery(initData)
	if err != nil {
		return 0, invalid("bad init data")
	}
	hash := vals.Get("hash")
	vals.Del("hash")
	keys := make([]string, 0, len(vals))
	for k := range vals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, k+"="+vals.Get(k))
	}
	secret := hmac.New(sha256.New, []byte("WebAppData"))
	secret.Write([]byte(token))
	m := hmac.New(sha256.New, secret.Sum(nil))
	m.Write([]byte(strings.Join(lines, "\n")))
	want := hex.EncodeToString(m.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(strings.ToLower(hash))) {
		return 0, invalid("the signature does not match")
	}
	at, _ := strconv.ParseInt(vals.Get("auth_date"), 10, 64)
	if d := s.now().Unix() - at; d > 300 || d < -60 {
		return 0, invalid("the data is too old: reopen the panel from the bot")
	}
	var user struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(vals.Get("user")), &user); err != nil || user.ID == 0 {
		return 0, invalid("no user in init data")
	}
	if !s.IsBotAdmin(ctx, user.ID) {
		return 0, invalid("this Telegram account is not a panel admin")
	}
	return user.ID, nil
}
