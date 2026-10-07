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
	login, _ := s.svc.Setting(r.Context(), service.SettingWebLogin, "")
	if login == "" {
		login = "admin"
	}
	if err := s.setCookie(w, r, login); err != nil {
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
		"backupTime": get(service.SettingBotBackupTime, service.DefaultBackupTime),
		"timezone":   get(service.SettingBotTimezone, service.DefaultTimezone),
		"alerts":     get(service.SettingBotAlerts, "true") != "false",
		"alertDelay": get(service.SettingBotAlertDelay, "60"),
		"status":     map[string]any{"running": false},
	}
	if s.cfg.Bot != nil {
		out["status"] = s.cfg.Bot.Status()
	}
	return out, nil
}

func (s *Server) botSave(r *http.Request) (any, error) {
	var in struct {
		Token      *string `json:"token"`
		BackupTime *string `json:"backupTime"`
		Timezone   *string `json:"timezone"`
		Alerts     *bool   `json:"alerts"`
		AlertDelay *int    `json:"alertDelay"`
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
