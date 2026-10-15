package service

import (
	"context"
	"regexp"
	"strings"

	"github.com/vyto4ka/vynel/internal/panel/store"
)

// How admins sign in to the web panel (docs/STEALTH.md §1).
const (
	SettingWebLoginMode    = "web.login_mode"    // password | link | miniapp
	SettingWebSessionPaths = "web.session_paths" // "true": every login link opens its own random path (link mode)

	ModePassword = "password" // login form, plus links and the Mini App
	ModeLink     = "link"     // no password: one-time links (bot, `vynel admin login-link`) and the Mini App
	ModeMiniApp  = "miniapp"  // only the Telegram Mini App
)

// LoginModes lists the modes.
var LoginModes = []string{ModePassword, ModeLink, ModeMiniApp}

// LoginMode returns how admins sign in. Panels that switched the login form off before modes
// existed are in link mode.
func (s *Service) LoginMode(ctx context.Context) string {
	v, _ := s.Setting(ctx, SettingWebLoginMode, "")
	switch v {
	case ModePassword, ModeLink, ModeMiniApp:
		return v
	}
	if pl, _ := s.Setting(ctx, SettingWebPasswordLogin, "true"); pl == "false" {
		return ModeLink
	}
	return ModePassword
}

// SetLoginMode changes how admins sign in. The Mini App needs the Telegram bot.
func (s *Service) SetLoginMode(ctx context.Context, actor Actor, mode string) error {
	mode = strings.TrimSpace(mode)
	switch mode {
	case ModePassword, ModeLink:
	case ModeMiniApp:
		if s.BotToken(ctx) == "" {
			return invalid("для входа только через Telegram сначала подключите бота (vynel admin bot token TOKEN) и привяжите себя")
		}
	default:
		return invalid("режим входа: password, link или miniapp")
	}
	if err := s.SetSetting(ctx, actor, SettingWebLoginMode, mode); err != nil {
		return err
	}
	return s.SetSetting(ctx, actor, SettingWebPasswordLogin, map[bool]string{true: "true", false: "false"}[mode == ModePassword])
}

// SessionPaths reports whether login links open random per-session paths (link mode only:
// the Mini App needs the panel's fixed path).
func (s *Service) SessionPaths(ctx context.Context) bool {
	if s.LoginMode(ctx) != ModeLink {
		return false
	}
	v, _ := s.Setting(ctx, SettingWebSessionPaths, "false")
	return v == "true"
}

// SetSessionPaths turns random per-session paths on or off.
func (s *Service) SetSessionPaths(ctx context.Context, actor Actor, on bool) error {
	if on && s.LoginMode(ctx) != ModeLink {
		return invalid("случайный путь на сессию работает только в режиме входа по ссылке (vynel admin web mode link)")
	}
	return s.SetSetting(ctx, actor, SettingWebSessionPaths, map[bool]string{true: "true", false: "false"}[on])
}

// WebPathKnown reports whether a path ("/x7.../") opens the panel: a login link waits for it
// or a live session uses it. Everything else on the panel domain is the decoy.
func (s *Service) WebPathKnown(ctx context.Context, path string) bool {
	if !s.SessionPaths(ctx) || !sessionPathRe.MatchString(path) {
		return false
	}
	now := s.now().Unix()
	if ok, err := store.LoginPathPending(ctx, s.st.DB, path, now); err == nil && ok {
		return true
	}
	ok, err := store.WebPathInUse(ctx, s.st.DB, path, now)
	return err == nil && ok
}

// WebSessionPath returns the path a session lives under ("" = the fixed one).
func (s *Service) WebSessionPath(ctx context.Context, id string) string {
	w, err := store.GetWebSession(ctx, s.st.DB, id)
	if err != nil {
		return ""
	}
	return w.Path
}

// sessionPathRe is the shape of a session path: what randomString(16) makes.
var sessionPathRe = regexp.MustCompile(`^/[A-Za-z0-9]{16}/$`)
