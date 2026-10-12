package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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

// Session rules (settings «Доступ к панели»).
const (
	SettingWebIdleMinutes = "web.idle_minutes" // end a session after this many minutes without actions; 0 = never
	SettingWebBindIP      = "web.bind_ip"      // end a session at once when its IP changes
	DefaultWebIdleMinutes = 5
)

// closingGrace: a page that said it was closing ends after this long unless it (or another tab of
// the same session) makes a request — a reload does at once.
const closingGrace = 20 * time.Second

// Why a web session ended.
const (
	EndLogout  = "logout"  // the admin signed out
	EndRevoked = "revoked" // ended from the sessions list or the bot
	EndIdle    = "idle"    // no actions for web.idle_minutes
	EndIP      = "ip"      // the request came from another IP
	EndClosed  = "closed"  // the page was closed
)

// EndReasonText explains to the admin why they have to sign in again.
func EndReasonText(reason string, idle int) string {
	switch reason {
	case EndIdle:
		return fmt.Sprintf("Сессия завершена: %d мин без действий", idle)
	case EndIP:
		return "Сессия завершена: изменился IP-адрес"
	case EndClosed:
		return "Сессия завершена: страница была закрыта"
	case EndRevoked:
		return "Сессия завершена из списка сессий"
	}
	return "Сессия завершена"
}

// Login methods.
const (
	LoginPassword = "password"
	LoginLink     = "link"     // one-time link from the bot or `vynel admin login-link`
	LoginTelegram = "telegram" // Telegram Mini App
)

// Notice is something the bot tells the admins about right away.
type Notice struct {
	Kind    string // login | ended
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
		CreatedAt: now, LastSeenAt: now, LastActiveAt: now, ExpiresAt: now + int64(ttl/time.Second)}
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

// WebIdleMinutes is the inactivity limit (0 = none).
func (s *Service) WebIdleMinutes(ctx context.Context) int {
	v, _ := s.Setting(ctx, SettingWebIdleMinutes, strconv.Itoa(DefaultWebIdleMinutes))
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 0 {
		return DefaultWebIdleMinutes
	}
	return n
}

func (s *Service) webBindIP(ctx context.Context) bool {
	v, _ := s.Setting(ctx, SettingWebBindIP, "true")
	return v != "false"
}

// WebSessionCheck decides whether a request may use a session. active: the request comes from
// something the admin did (not background polling). It returns "" when the session is fine, or
// why it has ended — the session is ended right there (another IP, too long without actions).
func (s *Service) WebSessionCheck(ctx context.Context, id, ip string, active bool) string {
	w, err := store.GetWebSession(ctx, s.st.DB, id)
	now := s.now().Unix()
	switch {
	case err != nil:
		return EndRevoked
	case w.RevokedAt != nil && w.EndReason != "":
		return w.EndReason
	case w.RevokedAt != nil:
		return EndRevoked
	case w.ExpiresAt <= now:
		return EndLogout
	}
	if ip != "" && w.IP != "" && ip != w.IP {
		if s.webBindIP(ctx) {
			s.endWebSession(ctx, w, EndIP, map[string]string{"new_ip": ip})
			return EndIP
		}
		_ = store.SetWebSessionIP(ctx, s.st.DB, id, ip)
	}
	if idle := s.WebIdleMinutes(ctx); idle > 0 && now-w.ActiveAt() > int64(idle)*60 {
		s.endWebSession(ctx, w, EndIdle, nil)
		return EndIdle
	}
	if now-w.LastSeenAt >= 15 || (active && now-w.LastActiveAt >= 5) || w.ClosingAt != nil {
		_ = store.TouchWebSession(ctx, s.st.DB, id, now, active)
	}
	return ""
}

// WebSessionClosing records that the session's page is being closed: unless a request follows
// within closingGrace (a reload, another tab), the session ends.
func (s *Service) WebSessionClosing(ctx context.Context, id string) error {
	return store.MarkWebSessionClosing(ctx, s.st.DB, id, s.now().Unix())
}

// ExpireWebSessions ends sessions that went quiet: no actions for web.idle_minutes, or a page
// that was closed. Run it every few seconds.
func (s *Service) ExpireWebSessions(ctx context.Context) {
	ws, err := s.WebSessions(ctx)
	if err != nil {
		return
	}
	now := s.now().Unix()
	idle := int64(s.WebIdleMinutes(ctx)) * 60
	for _, w := range ws {
		switch {
		case idle > 0 && now-w.ActiveAt() > idle:
			s.endWebSession(ctx, w, EndIdle, nil)
		case w.ClosingAt != nil && now-*w.ClosingAt >= int64(closingGrace/time.Second):
			s.endWebSession(ctx, w, EndClosed, nil)
		}
	}
}

// WebSessionsLoop runs ExpireWebSessions until ctx ends.
func (s *Service) WebSessionsLoop(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.ExpireWebSessions(ctx)
		}
	}
}

// endWebSession ends a session for a reason and tells the bot (it deletes its sign-in message).
func (s *Service) endWebSession(ctx context.Context, w *store.WebSession, reason string, extra map[string]string) {
	diff := map[string]string{"session": w.ID, "reason": reason, "ip": w.IP}
	for k, v := range extra {
		diff[k] = v
	}
	ended := false
	err := s.mutate(ctx, change{actor: ActorSystem, action: "web.session_end", entity: "web", diff: diff},
		func(q store.DBTX) error {
			var err error
			ended, err = store.RevokeWebSession(ctx, q, w.ID, reason, s.now().Unix())
			return err
		})
	if err == nil && ended {
		s.notify(Notice{Kind: "ended", Session: w})
	}
}

// WebSessions lists active sessions.
func (s *Service) WebSessions(ctx context.Context) ([]*store.WebSession, error) {
	return store.ListWebSessions(ctx, s.st.DB, s.now().Unix())
}

// WebSessionIPs are the addresses of signed-in admins: the firewall never bans them.
func (s *Service) WebSessionIPs(ctx context.Context) []string {
	ws, err := s.WebSessions(ctx)
	if err != nil {
		return nil
	}
	var ips []string
	for _, w := range ws {
		if w.IP != "" {
			ips = append(ips, w.IP)
		}
	}
	return ips
}

// EndWebSessions ends one session, or all but keep when id is empty. It returns how many ended.
func (s *Service) EndWebSessions(ctx context.Context, actor Actor, id, keep string) (int64, error) {
	return s.endWebSessions(ctx, actor, id, keep, EndRevoked)
}

// Logout ends the admin's own session.
func (s *Service) Logout(ctx context.Context, actor Actor, id string) error {
	_, err := s.endWebSessions(ctx, actor, id, "", EndLogout)
	return err
}

func (s *Service) endWebSessions(ctx context.Context, actor Actor, id, keep, reason string) (int64, error) {
	var ended []*store.WebSession
	err := s.mutate(ctx, change{actor: actor, action: "web.logout", entity: "web", diff: map[string]string{"session": id, "keep": keep, "reason": reason}},
		func(q store.DBTX) error {
			ws, err := store.ListWebSessions(ctx, q, s.now().Unix())
			if err != nil {
				return err
			}
			for _, w := range ws {
				if (id != "" && w.ID != id) || (id == "" && w.ID == keep) {
					continue
				}
				ok, err := store.RevokeWebSession(ctx, q, w.ID, reason, s.now().Unix())
				if err != nil {
					return err
				}
				if ok {
					ended = append(ended, w)
				}
			}
			return nil
		})
	if err != nil {
		return 0, err
	}
	for _, w := range ended {
		s.notify(Notice{Kind: "ended", Session: w})
	}
	return int64(len(ended)), nil
}

// RememberLoginNotice records a bot message about a sign-in, to delete it when the session ends.
func (s *Service) RememberLoginNotice(ctx context.Context, session string, chat, msg int64) error {
	return store.AddWebSessionNotice(ctx, s.st.DB, session, chat, msg)
}

// EndedLoginNotices returns (and forgets) the bot messages about sessions that have ended.
func (s *Service) EndedLoginNotices(ctx context.Context) ([][2]int64, error) {
	ids, err := store.EndedWebSessionsWithNotices(ctx, s.st.DB, s.now().Unix())
	if err != nil {
		return nil, err
	}
	var out [][2]int64
	for _, id := range ids {
		ns, err := store.TakeWebSessionNotices(ctx, s.st.DB, id)
		if err != nil {
			return out, err
		}
		out = append(out, ns...)
	}
	return out, nil
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
