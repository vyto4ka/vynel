package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/store"
)

// Telegram bot settings (docs/ARCHITECTURE.md §10). The bot is admin-only: it answers bound
// admins and nobody else.
const (
	SettingBotToken       = "bot.token"        // BotFather token (secret)
	SettingBotAdmins      = "bot.admins"       // JSON []BotAdmin
	settingBotBindCode    = "bot.bind_code"    // "<code>|<unix expiry>" (secret)
	SettingBotBackupTime  = "bot.backup_time"  // HH:MM, empty = no nightly backups
	SettingBotTimezone    = "bot.timezone"     // IANA name for times in messages and the backup schedule
	SettingBotAlerts      = "bot.alerts"       // "false" = no node down/up messages
	SettingBotAlertDelay  = "bot.alert_delay"  // seconds a node must be away before an alert (default 60)
	SettingBotAlertState  = "bot.alert_state"  // JSON: messages being edited (survives restarts)
	SettingBotSummaryTime = "bot.summary_time" // HH:MM of the daily summary, "off" = none
	SettingBotSummaryLast = "bot.summary_last" // date of the last summary
)

// Defaults of the bot settings.
const (
	DefaultBackupTime  = "23:00"
	DefaultSummaryTime = "10:00"
	DefaultTimezone    = "Europe/Moscow"
	BindCodeTTL        = 15 * time.Minute
	LoginLinkTTL       = time.Minute
)

func init() {
	secretSettings[SettingBotToken] = true
	secretSettings[settingBotBindCode] = true
}

// BotAdmin is a Telegram account allowed to use the bot.
type BotAdmin struct {
	ID       int64  `json:"id"` // Telegram user id (= private chat id)
	Name     string `json:"name"`
	Username string `json:"username,omitempty"`
	BoundAt  int64  `json:"boundAt"`
}

// BotToken returns the bot token ("" = no bot).
func (s *Service) BotToken(ctx context.Context) string {
	t, _ := s.Setting(ctx, SettingBotToken, "")
	return strings.TrimSpace(t)
}

// SetBotToken stores the token after a format check (the bot checks it with Telegram).
func (s *Service) SetBotToken(ctx context.Context, actor Actor, token string) error {
	token = strings.TrimSpace(token)
	if token != "" {
		id, secret, ok := strings.Cut(token, ":")
		if _, err := strconv.ParseInt(id, 10, 64); err != nil || !ok || len(secret) < 20 {
			return invalid("this does not look like a bot token from @BotFather (123456789:AA…)")
		}
	}
	return s.setSecret(ctx, actor, SettingBotToken, token)
}

// BotAdmins lists the bound admins.
func (s *Service) BotAdmins(ctx context.Context) ([]BotAdmin, error) {
	raw, err := s.Setting(ctx, SettingBotAdmins, "")
	if err != nil || raw == "" {
		return []BotAdmin{}, err
	}
	var out []BotAdmin
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return []BotAdmin{}, nil
	}
	return out, nil
}

// IsBotAdmin reports whether a Telegram user is a bound admin.
func (s *Service) IsBotAdmin(ctx context.Context, id int64) bool {
	as, _ := s.BotAdmins(ctx)
	for _, a := range as {
		if a.ID == id {
			return true
		}
	}
	return false
}

func (s *Service) saveBotAdmins(ctx context.Context, actor Actor, as []BotAdmin) error {
	b, _ := json.Marshal(as)
	return s.SetSetting(ctx, actor, SettingBotAdmins, string(b))
}

// RemoveBotAdmin unbinds an admin.
func (s *Service) RemoveBotAdmin(ctx context.Context, actor Actor, id int64) error {
	as, err := s.BotAdmins(ctx)
	if err != nil {
		return err
	}
	out := as[:0]
	found := false
	for _, a := range as {
		if a.ID == id {
			found = true
			continue
		}
		out = append(out, a)
	}
	if !found {
		return ErrNotFound
	}
	return s.saveBotAdmins(ctx, actor, out)
}

// NewBindCode creates a one-time code that binds the Telegram account sending "/start CODE".
func (s *Service) NewBindCode(ctx context.Context, actor Actor) (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(100_000_000))
	if err != nil {
		return "", err
	}
	code := fmt.Sprintf("%08d", n.Int64())
	exp := s.now().Add(BindCodeTTL).Unix()
	return code, s.setSecret(ctx, actor, settingBotBindCode, code+"|"+strconv.FormatInt(exp, 10))
}

// BindBotAdmin consumes a bind code and binds the account. It returns false for a wrong or
// expired code.
func (s *Service) BindBotAdmin(ctx context.Context, code string, a BotAdmin) (bool, error) {
	raw, _ := s.Setting(ctx, settingBotBindCode, "")
	want, expStr, _ := strings.Cut(raw, "|")
	exp, _ := strconv.ParseInt(expStr, 10, 64)
	code = strings.TrimSpace(code)
	if want == "" || len(code) != len(want) || subtle.ConstantTimeCompare([]byte(code), []byte(want)) != 1 || s.now().Unix() > exp {
		return false, nil
	}
	actor := Actor{Kind: "bot", ID: strconv.FormatInt(a.ID, 10)}
	if err := s.setSecret(ctx, actor, settingBotBindCode, ""); err != nil {
		return false, err
	}
	as, err := s.BotAdmins(ctx)
	if err != nil {
		return false, err
	}
	a.BoundAt = s.now().Unix()
	out := []BotAdmin{a}
	for _, x := range as {
		if x.ID != a.ID {
			out = append(out, x)
		}
	}
	return true, s.saveBotAdmins(ctx, actor, out)
}

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

// CreateLoginToken issues a one-time web login token valid for LoginLinkTTL. Only its hash is
// stored.
func (s *Service) CreateLoginToken(ctx context.Context, actor Actor) (string, time.Time, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", time.Time{}, err
	}
	tok := base64.RawURLEncoding.EncodeToString(b)
	exp := s.now().Add(LoginLinkTTL)
	issuer := actor.Kind + ":" + actor.ID
	err := s.mutate(ctx, change{actor: actor, action: "web.login_link", entity: "web"},
		func(q store.DBTX) error { return store.AddLoginToken(ctx, q, hashToken(tok), issuer, exp.Unix()) })
	return tok, exp, err
}

// UseLoginToken consumes a login token. It returns who issued it; ErrNotFound for an unknown,
// used or expired token.
func (s *Service) UseLoginToken(ctx context.Context, tok string) (string, error) {
	if len(tok) < 20 || len(tok) > 100 {
		return "", ErrNotFound
	}
	var issuer string
	err := s.mutate(ctx, change{actor: Actor{Kind: "web"}, action: "web.login_link_used", entity: "web"},
		func(q store.DBTX) error {
			var err error
			issuer, err = store.UseLoginToken(ctx, q, hashToken(tok), s.now().Unix())
			return err
		})
	return issuer, err
}

// LoginLink is the panel URL with a fresh one-time token. The token is in the #fragment: browsers
// never send it to the server with the page request, so link previews cannot use it up and it
// does not end up in access logs. The page posts it to the API.
func (s *Service) LoginLink(ctx context.Context, actor Actor) (string, time.Time, error) {
	base, err := s.WebURL(ctx)
	if err != nil {
		return "", time.Time{}, err
	}
	if base == "" {
		return "", time.Time{}, invalid("the panel has no domain (sub.domain / web.domain)")
	}
	tok, exp, err := s.CreateLoginToken(ctx, actor)
	if err != nil {
		return "", time.Time{}, err
	}
	return base + "#/magic/" + tok, exp, nil
}

// BotLocation is the timezone for bot messages and the backup schedule.
func (s *Service) BotLocation(ctx context.Context) *time.Location {
	name, _ := s.Setting(ctx, SettingBotTimezone, DefaultTimezone)
	if loc, err := time.LoadLocation(name); err == nil {
		return loc
	}
	return time.UTC
}
