package webapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/service"
)

func (s *Server) botRoutes() {
	s.api.HandleFunc("POST /api/login/magic", s.magicLogin)
	s.api.HandleFunc("POST /api/login/telegram", s.telegramLogin)
	s.api.HandleFunc("GET /api/login/options", s.loginOptions)
	s.handle("GET /api/sessions", s.sessions)
	s.handle("DELETE /api/sessions/{id}", s.endSession)
	s.handle("POST /api/sessions/end-others", s.endOtherSessions)
	s.handle("PUT /api/account/password-login", s.passwordLogin)
	s.handle("GET /api/bot", s.botInfo)
	s.handle("PUT /api/bot", s.botSave)
	s.handle("POST /api/bot/code", s.botCode)
	s.handle("DELETE /api/bot/admins/{id}", s.botUnbind)
	s.handle("POST /api/bot/backup", s.botBackup)
	s.handle("GET /api/backup", s.downloadBackup)
}

// magicLogin exchanges a one-time token from the Telegram bot (or `vynel admin login-link`) for
// a session. The token travels in the URL fragment, so only the page itself can post it here.
func (s *Server) magicLogin(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get(csrfHeader) != "1" {
		writeError(w, &httpError{http.StatusForbidden, "missing " + csrfHeader + " header"})
		return
	}
	var in struct{ Token string }
	if err := decode(r, &in); err != nil {
		writeError(w, &httpError{http.StatusBadRequest, err.Error()})
		return
	}
	s.loginMu.Lock()
	issuer, err := s.svc.UseLoginToken(r.Context(), strings.TrimSpace(in.Token))
	if err != nil {
		time.Sleep(700 * time.Millisecond)
	}
	s.loginMu.Unlock()
	if err != nil {
		s.log.Warn("web magic login failed", "ip", clientIP(r))
		writeError(w, &httpError{http.StatusUnauthorized, "ссылка недействительна: она живёт минуту и работает один раз — запросите новую"})
		return
	}
	login, err := s.startSession(w, r, service.LoginLink, issuer)
	if err != nil {
		writeError(w, &httpError{http.StatusInternalServerError, err.Error()})
		return
	}
	s.log.Info("web login by one-time link", "ip", clientIP(r), "issued_by", issuer)
	s.respond(w, r, func(*http.Request) (any, error) { return map[string]string{"login": login}, nil })
}

func (s *Server) botInfo(r *http.Request) (any, error) {
	ctx := r.Context()
	admins, err := s.svc.BotAdmins(ctx)
	if err != nil {
		return nil, err
	}
	get := func(k, def string) string { v, _ := s.svc.Setting(ctx, k, def); return v }
	out := map[string]any{
		"hasToken": s.svc.BotToken(ctx) != "", "admins": admins,
		"backupTime":  get(service.SettingBotBackupTime, service.DefaultBackupTime),
		"summaryTime": get(service.SettingBotSummaryTime, service.DefaultSummaryTime),
		"timezone":    get(service.SettingBotTimezone, service.DefaultTimezone),
		"alerts":      get(service.SettingBotAlerts, "true") != "false",
		"alertDelay":  get(service.SettingBotAlertDelay, "60"),
		"status":      map[string]any{"running": false},
	}
	if s.cfg.Bot != nil {
		out["status"] = s.cfg.Bot.Status()
	}
	return out, nil
}

func (s *Server) botSave(r *http.Request) (any, error) {
	var in struct {
		Token       *string `json:"token"`
		BackupTime  *string `json:"backupTime"`
		SummaryTime *string `json:"summaryTime"`
		Timezone    *string `json:"timezone"`
		Alerts      *bool   `json:"alerts"`
		AlertDelay  *int    `json:"alertDelay"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	ctx, a := r.Context(), actor(r)
	if in.Token != nil {
		if err := s.svc.SetBotToken(ctx, a, *in.Token); err != nil {
			return nil, err
		}
	}
	if in.BackupTime != nil {
		v := strings.TrimSpace(*in.BackupTime)
		if v != "" && v != "off" {
			if _, err := time.Parse("15:04", v); err != nil {
				return nil, badRequest("время бэкапа — ЧЧ:ММ, например 23:00, или пусто, чтобы выключить")
			}
		}
		if v == "" {
			v = "off"
		}
		if err := s.svc.SetSetting(ctx, a, service.SettingBotBackupTime, v); err != nil {
			return nil, err
		}
	}
	if in.SummaryTime != nil {
		v := strings.TrimSpace(*in.SummaryTime)
		if v != "" && v != "off" {
			if _, err := time.Parse("15:04", v); err != nil {
				return nil, badRequest("время сводки — ЧЧ:ММ, например 10:00, или пусто, чтобы выключить")
			}
		}
		if v == "" {
			v = "off"
		}
		if err := s.svc.SetSetting(ctx, a, service.SettingBotSummaryTime, v); err != nil {
			return nil, err
		}
	}
	if in.Timezone != nil {
		if _, err := time.LoadLocation(*in.Timezone); err != nil || *in.Timezone == "" {
			return nil, badRequest("неизвестный часовой пояс, пример: Europe/Moscow")
		}
		if err := s.svc.SetSetting(ctx, a, service.SettingBotTimezone, *in.Timezone); err != nil {
			return nil, err
		}
	}
	if in.Alerts != nil {
		if err := s.svc.SetSetting(ctx, a, service.SettingBotAlerts, strconv.FormatBool(*in.Alerts)); err != nil {
			return nil, err
		}
	}
	if in.AlertDelay != nil {
		if *in.AlertDelay < 0 || *in.AlertDelay > 3600 {
			return nil, badRequest("задержка — от 0 до 3600 секунд")
		}
		if err := s.svc.SetSetting(ctx, a, service.SettingBotAlertDelay, strconv.Itoa(*in.AlertDelay)); err != nil {
			return nil, err
		}
	}
	return s.botInfo(r)
}

func (s *Server) botCode(r *http.Request) (any, error) {
	code, err := s.svc.NewBindCode(r.Context(), actor(r))
	if err != nil {
		return nil, err
	}
	out := map[string]any{"code": code, "minutes": int(service.BindCodeTTL.Minutes())}
	if s.cfg.Bot != nil {
		if u := s.cfg.Bot.Status().Username; u != "" {
			out["link"] = "https://t.me/" + u + "?start=" + code
			out["username"] = u
		}
	}
	return out, nil
}

func (s *Server) botUnbind(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	return nil, s.svc.RemoveBotAdmin(r.Context(), actor(r), id)
}

func (s *Server) botBackup(r *http.Request) (any, error) {
	if s.cfg.Bot == nil || !s.cfg.Bot.Status().Running {
		return nil, badRequest("бот не запущен — задайте токен")
	}
	return nil, s.cfg.Bot.SendBackup(r.Context())
}

func (s *Server) downloadBackup(r *http.Request) (any, error) {
	if s.cfg.Bot == nil {
		return nil, badRequest("бэкап недоступен")
	}
	name, data, _, err := s.cfg.Bot.Backup(r.Context())
	if err != nil {
		return nil, err
	}
	w := r.Context().Value(writerKey{}).(http.ResponseWriter)
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	return rawResponse{"application/gzip", data}, nil
}

// telegramLogin signs in an admin who opened the panel as a Telegram Mini App from the bot.
func (s *Server) telegramLogin(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get(csrfHeader) != "1" {
		writeError(w, &httpError{http.StatusForbidden, "missing " + csrfHeader + " header"})
		return
	}
	var in struct{ InitData string }
	if err := decode(r, &in); err != nil {
		writeError(w, &httpError{http.StatusBadRequest, err.Error()})
		return
	}
	s.loginMu.Lock()
	id, err := s.svc.TelegramAppLogin(r.Context(), in.InitData)
	if err != nil {
		time.Sleep(700 * time.Millisecond)
	}
	s.loginMu.Unlock()
	if err != nil {
		s.log.Warn("web telegram login failed", "ip", clientIP(r), "err", err)
		writeError(w, &httpError{http.StatusUnauthorized, "Telegram не подтвердил вход: " + strings.TrimPrefix(err.Error(), service.ErrInvalid.Error()+": ")})
		return
	}
	login, err := s.startSession(w, r, service.LoginTelegram, "bot:"+strconv.FormatInt(id, 10))
	if err != nil {
		writeError(w, &httpError{http.StatusInternalServerError, err.Error()})
		return
	}
	s.log.Info("web login from telegram mini app", "ip", clientIP(r), "telegram_id", id)
	s.respond(w, r, func(*http.Request) (any, error) { return map[string]string{"login": login}, nil })
}

// loginOptions tells the login page which ways in work.
func (s *Server) loginOptions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	bot := ""
	if s.cfg.Bot != nil {
		bot = s.cfg.Bot.Status().Username
	}
	s.respond(w, r, func(*http.Request) (any, error) {
		return map[string]any{"password": s.svc.PasswordLoginEnabled(ctx), "bot": bot}, nil
	})
}

func (s *Server) sessions(r *http.Request) (any, error) {
	ws, err := s.svc.WebSessions(r.Context())
	if err != nil {
		return nil, err
	}
	cur := current(r).ID
	out := make([]map[string]any, 0, len(ws))
	for _, w := range ws {
		out = append(out, map[string]any{"id": w.ID, "method": w.Method, "actor": w.Actor, "ip": w.IP, "userAgent": w.UserAgent,
			"createdAt": w.CreatedAt, "lastSeenAt": w.LastSeenAt, "expiresAt": w.ExpiresAt, "current": w.ID == cur})
	}
	return map[string]any{"sessions": out, "passwordLogin": s.svc.PasswordLoginEnabled(r.Context())}, nil
}

func (s *Server) endSession(r *http.Request) (any, error) {
	id := r.PathValue("id")
	if id == current(r).ID {
		return nil, badRequest("это текущая сессия — нажмите «Выйти»")
	}
	_, err := s.svc.EndWebSessions(r.Context(), actor(r), id, "")
	return nil, err
}

func (s *Server) endOtherSessions(r *http.Request) (any, error) {
	n, err := s.svc.EndWebSessions(r.Context(), actor(r), "", current(r).ID)
	return map[string]int64{"ended": n}, err
}

func (s *Server) passwordLogin(r *http.Request) (any, error) {
	var in struct{ On bool }
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	if err := s.svc.SetPasswordLogin(r.Context(), actor(r), in.On); err != nil {
		return nil, err
	}
	return map[string]bool{"passwordLogin": in.On}, nil
}
