package service

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"math/big"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/vyto4ka/vynel/internal/jointoken"
	"github.com/vyto4ka/vynel/internal/panel/store"
)

// Web panel settings (docs/STEALTH.md §2.2). The panel lives under a secret path on the
// subscription domain (or web.domain), proxied by Caddy to web.listen.
const (
	SettingWebPath      = "web.path"   // secret path, e.g. /k9Qm2xT7aB3c/
	SettingWebListen    = "web.listen" // internal listener behind Caddy
	SettingWebDomain    = "web.domain" // optional; empty = sub.domain
	SettingWebLogin     = "web.login"
	settingWebPassword  = "web.password_hash" // bcrypt
	settingWebSessionKy = "web.session_key"   // HMAC key of session cookies
)

// DefaultWebListen is the panel UI listener behind Caddy.
const DefaultWebListen = "127.0.0.1:2097"

// secretSettings never leave the server through the web API.
var secretSettings = map[string]bool{settingWebPassword: true, settingWebSessionKy: true}

// IsSecretSetting reports whether a setting must not be shown or changed from the web.
func IsSecretSetting(key string) bool { return secretSettings[key] }

const alnum = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789" // no 0/O, 1/l/I

func randomString(n int) (string, error) {
	b := make([]byte, n)
	for i := range b {
		k, err := rand.Int(rand.Reader, big.NewInt(int64(len(alnum))))
		if err != nil {
			return "", err
		}
		b[i] = alnum[k.Int64()]
	}
	return string(b), nil
}

// setSecret stores a setting without putting its value into the audit log.
func (s *Service) setSecret(ctx context.Context, actor Actor, key, value string) error {
	return s.mutate(ctx, change{actor: actor, action: "setting.set", entity: "setting", diff: map[string]string{key: "***"}},
		func(q store.DBTX) error { return store.SetSetting(ctx, q, key, value) })
}

// EnsureWebDefaults generates the secret path and the session key on first start.
func (s *Service) EnsureWebDefaults(ctx context.Context) error {
	if p, err := s.Setting(ctx, SettingWebPath, ""); err != nil {
		return err
	} else if p == "" {
		r, err := randomString(12)
		if err != nil {
			return err
		}
		if err := s.SetSetting(ctx, ActorSystem, SettingWebPath, "/"+r+"/"); err != nil {
			return err
		}
	}
	if k, err := s.Setting(ctx, settingWebSessionKy, ""); err != nil {
		return err
	} else if k == "" {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return err
		}
		return s.setSecret(ctx, ActorSystem, settingWebSessionKy, hex.EncodeToString(b))
	}
	return nil
}

// WebPath returns the normalized secret path ("/xxx/").
func (s *Service) WebPath(ctx context.Context) (string, error) {
	p, err := s.Setting(ctx, SettingWebPath, "")
	if err != nil {
		return "", err
	}
	return normalizePath(p), nil
}

func normalizePath(p string) string {
	p = strings.Trim(strings.TrimSpace(p), "/")
	if p == "" {
		return ""
	}
	return "/" + p + "/"
}

// WebURL is the public address of the panel UI, or "" when no domain is configured.
func (s *Service) WebURL(ctx context.Context) (string, error) {
	path, err := s.WebPath(ctx)
	if err != nil || path == "" {
		return "", err
	}
	domain, _ := s.Setting(ctx, SettingWebDomain, "")
	if domain == "" {
		domain, _ = s.Setting(ctx, SettingSubDomain, "")
	}
	if domain == "" {
		return "", nil
	}
	port, _ := s.Setting(ctx, SettingSubPort, "443")
	if port != "" && port != "443" {
		domain += ":" + port
	}
	return "https://" + domain + path, nil
}

// SetAdminCredentials sets the admin login and a new generated password, which it returns.
// An empty login keeps the current one ("admin" if none).
func (s *Service) SetAdminCredentials(ctx context.Context, actor Actor, login string) (string, error) {
	pw, err := randomString(16)
	if err != nil {
		return "", err
	}
	return pw, s.SetAdminPassword(ctx, actor, login, pw)
}

// SetAdminPassword sets the admin login and password.
func (s *Service) SetAdminPassword(ctx context.Context, actor Actor, login, password string) error {
	login = strings.TrimSpace(login)
	if login == "" {
		login, _ = s.Setting(ctx, SettingWebLogin, "")
	}
	if login == "" {
		login = "admin"
	}
	if !usernameRe.MatchString(login) {
		return invalid("login must be 1-64 letters, digits or _ . - @")
	}
	if len(password) < 8 {
		return invalid("password must be at least 8 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if err := s.SetSetting(ctx, actor, SettingWebLogin, login); err != nil {
		return err
	}
	return s.setSecret(ctx, actor, settingWebPassword, string(hash))
}

// ErrBadLogin is returned for a wrong login or password.
var ErrBadLogin = errors.New("wrong login or password")

var dummyHash = sync.OnceValue(func() []byte {
	h, _ := bcrypt.GenerateFromPassword([]byte("dummy password"), bcrypt.DefaultCost)
	return h
})

// CheckAdmin verifies the credentials.
func (s *Service) CheckAdmin(ctx context.Context, login, password string) error {
	want, _ := s.Setting(ctx, SettingWebLogin, "")
	hash, _ := s.Setting(ctx, settingWebPassword, "")
	if hash == "" {
		// Spend the same time as a real check so the absence of a password is not observable.
		_ = bcrypt.CompareHashAndPassword(dummyHash(), []byte(password))
		return ErrBadLogin
	}
	errPw := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	if subtle.ConstantTimeCompare([]byte(login), []byte(want)) != 1 || errPw != nil {
		return ErrBadLogin
	}
	return nil
}

// SessionKey returns the key for signing admin sessions: the stored random key bound to the
// current password hash and login, so changing either logs every session out.
func (s *Service) SessionKey(ctx context.Context) ([]byte, error) {
	k, err := s.Setting(ctx, settingWebSessionKy, "")
	if err != nil {
		return nil, err
	}
	if k == "" {
		return nil, errors.New("session key is not initialized")
	}
	hash, _ := s.Setting(ctx, settingWebPassword, "")
	login, _ := s.Setting(ctx, SettingWebLogin, "")
	return []byte(k + "|" + hash + "|" + login), nil
}

// HasAdmin reports whether admin credentials are set.
func (s *Service) HasAdmin(ctx context.Context) bool {
	h, _ := s.Setting(ctx, settingWebPassword, "")
	return h != ""
}

// SettingInstallCommand is how a new server runs the installer; the installer stores it.
const SettingInstallCommand = "install.command"

// DefaultInstallCommand is used until the installer has stored its own.
const DefaultInstallCommand = "bash <(curl -fsSL https://raw.githubusercontent.com/vyto4ka/vynel/claude/magical-hamilton-9vnx7n/scripts/install.sh)"

// NodeInstallCommand is the one-liner that installs a node with a join token.
func (s *Service) NodeInstallCommand(ctx context.Context, token string) string {
	cmd, _ := s.Setting(ctx, SettingInstallCommand, "")
	if strings.TrimSpace(cmd) == "" {
		cmd = DefaultInstallCommand
	}
	return cmd + " --mode node --token " + token
}

// JoinToken builds the node join token for an install secret (CA fingerprint from the caller).
func (s *Service) JoinToken(ctx context.Context, caFingerprint, secret string) (string, error) {
	addr, err := s.Setting(ctx, SettingGatewayAddr, "")
	if err != nil {
		return "", err
	}
	sni, err := s.Setting(ctx, SettingGatewaySNI, "")
	if err != nil {
		return "", err
	}
	if addr == "" || sni == "" {
		return "", invalid("the gateway address is unknown: start the panel with --public-addr HOST:PORT or set gateway.addr")
	}
	return jointoken.Token{Addr: addr, SNI: sni, CAFingerprint: caFingerprint, Secret: secret}.Encode(), nil
}

// ---- small additions used by the web UI ----

// UserDetailsInput changes the descriptive fields of a user. Nil fields stay as they are.
type UserDetailsInput struct {
	Note          *string
	HWIDLimit     *int64 // <0 = back to the default limit
	ClientType    *string
	ResetStrategy *string
}

// SetUserDetails updates note, HWID limit, client type and reset strategy.
func (s *Service) SetUserDetails(ctx context.Context, actor Actor, id int64, in UserDetailsInput) (*store.User, error) {
	if in.ResetStrategy != nil {
		if err := checkReset(in.ResetStrategy); err != nil {
			return nil, err
		}
	}
	return s.updateUser(ctx, actor, id, "user.set_details", in, func(_ store.DBTX, u *store.User) error {
		if in.Note != nil {
			u.Note = *in.Note
		}
		if in.HWIDLimit != nil {
			if *in.HWIDLimit < 0 {
				u.HWIDLimit = nil
			} else {
				v := *in.HWIDLimit
				u.HWIDLimit = &v
			}
		}
		if in.ClientType != nil {
			u.ClientType = firstNonEmpty(*in.ClientType, "auto")
		}
		if in.ResetStrategy != nil {
			u.ResetStrategy = *in.ResetStrategy
		}
		return nil
	})
}

// UpdateUserTemplate replaces a template.
func (s *Service) UpdateUserTemplate(ctx context.Context, actor Actor, id int64, in UserTemplateInput) (*store.UserTemplate, error) {
	if strings.TrimSpace(in.Name) == "" {
		return nil, invalid("template name is required")
	}
	if err := checkReset(&in.ResetStrategy); err != nil {
		return nil, err
	}
	if in.ExpireMonths < 0 || in.ExpireDays < 0 {
		return nil, invalid("expiry must not be negative")
	}
	var t *store.UserTemplate
	err := s.mutate(ctx, change{actor: actor, action: "user_template.update", entity: "user_template", entityID: func() int64 { return id }, diff: in},
		func(q store.DBTX) error {
			var err error
			if t, err = store.GetUserTemplate(ctx, q, id); err != nil {
				return err
			}
			for _, g := range in.GroupIDs {
				if _, err := store.GetGroup(ctx, q, g); err != nil {
					return err
				}
			}
			wasDefault := t.IsDefault
			t.Name, t.IsDefault, t.ExpireMonths, t.ExpireDays = strings.TrimSpace(in.Name), in.IsDefault || wasDefault, in.ExpireMonths, in.ExpireDays
			t.TrafficLimitBytes, t.ResetStrategy, t.HWIDLimit = nilIfNonPositive(in.TrafficLimitBytes), in.ResetStrategy, in.HWIDLimit
			t.ClientType, t.GroupIDs, t.Note = firstNonEmpty(in.ClientType, "auto"), in.GroupIDs, in.Note
			return store.UpdateUserTemplate(ctx, q, t)
		})
	return t, err
}

// DeleteUserTemplate removes a template; the default one can only go when it is the last.
func (s *Service) DeleteUserTemplate(ctx context.Context, actor Actor, id int64) error {
	return s.mutate(ctx, change{actor: actor, action: "user_template.delete", entity: "user_template", entityID: func() int64 { return id }},
		func(q store.DBTX) error {
			t, err := store.GetUserTemplate(ctx, q, id)
			if err != nil {
				return err
			}
			all, err := store.ListUserTemplates(ctx, q)
			if err != nil {
				return err
			}
			if t.IsDefault && len(all) > 1 {
				return invalid("make another template the default first")
			}
			return store.DeleteUserTemplate(ctx, q, id)
		})
}

// UpdateGroup renames a group or changes its description.
func (s *Service) UpdateGroup(ctx context.Context, actor Actor, id int64, name, description string) (*store.Group, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, invalid("group name is required")
	}
	var g *store.Group
	err := s.mutate(ctx, change{actor: actor, action: "group.update", entity: "group", entityID: func() int64 { return id }, event: EvGroupChanged,
		diff: map[string]string{"name": name}},
		func(q store.DBTX) error {
			var err error
			if g, err = store.GetGroup(ctx, q, id); err != nil {
				return err
			}
			g.Name, g.Description = name, description
			return store.UpdateGroup(ctx, q, g)
		})
	return g, err
}

// DayTraffic is the traffic of one day.
type DayTraffic struct {
	Day  int64 // unix, start of the UTC day
	Up   int64
	Down int64
}

// TrafficByDay returns the total traffic per day for the last days (oldest first, gaps filled).
// With userID > 0 it is the traffic of one user.
func (s *Service) TrafficByDay(ctx context.Context, userID int64, days int) ([]DayTraffic, error) {
	now := s.now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	since := today.AddDate(0, 0, -(days - 1)).Unix()
	rows, err := store.TrafficByDay(ctx, s.st.DB, userID, since)
	if err != nil {
		return nil, err
	}
	byDay := map[int64]store.DayTotal{}
	for _, r := range rows {
		byDay[r.Day] = r
	}
	out := make([]DayTraffic, 0, days)
	for d := since; d <= today.Unix(); d += 86400 {
		r := byDay[d]
		out = append(out, DayTraffic{Day: d, Up: r.Up, Down: r.Down})
	}
	return out, nil
}

// Audit returns the newest audit entries.
func (s *Service) Audit(ctx context.Context, limit int) ([]store.AuditEntry, error) {
	return store.ListAudit(ctx, s.st.DB, limit)
}

// GroupMemberCounts returns group id -> number of users.
func (s *Service) GroupMemberCounts(ctx context.Context) (map[int64]int, error) {
	return store.GroupMemberCounts(ctx, s.st.DB)
}

// Settings returns every stored setting except secrets.
func (s *Service) Settings(ctx context.Context) (map[string]string, error) {
	all, err := store.ListSettings(ctx, s.st.DB)
	if err != nil {
		return nil, err
	}
	for k := range secretSettings {
		delete(all, k)
	}
	return all, nil
}
