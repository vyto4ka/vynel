// Package webapi serves the admin web UI and its JSON API under the secret path
// (docs/STEALTH.md §2.2). It is a thin transport over internal/panel/service.
package webapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/vyto4ka/vynel/internal/clientip"
	"github.com/vyto4ka/vynel/internal/decoy"
	"github.com/vyto4ka/vynel/internal/panel/bot"
	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/update"
	"github.com/vyto4ka/vynel/web"
)

// Config configures the web server.
type Config struct {
	Service       *service.Service
	CAFingerprint func() string    // for node join tokens
	Connected     func(int64) bool // live node sessions; nil = guess from last_seen
	Version       string
	Log           *slog.Logger
	UI            fs.FS    // built UI; nil = web.Dist
	Bot           *bot.Bot // Telegram bot (status, backups); nil in tests
	// NodeAction runs an admin action (service.NodeActions) on a connected node; nil = unavailable.
	NodeAction func(ctx context.Context, nodeID int64, action string) (detail string, err error)
	// UpdateStatus is what a node last reported about its update; nil = never reports.
	UpdateStatus func(nodeID int64) *update.Status
}

// Server is the web panel.
type Server struct {
	cfg   Config
	svc   *service.Service
	log   *slog.Logger
	ui    fs.FS
	api   *http.ServeMux
	guard *loginGuard // sign-in attempts per address
	upd   updates
}

const (
	cookieName = "vynel_session"
	sessionTTL = 14 * 24 * time.Hour
	csrfHeader = "X-Vynel"
)

// New creates the server.
func New(cfg Config) *Server {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	ui := cfg.UI
	if ui == nil {
		ui, _ = fs.Sub(web.Dist, "dist")
	}
	s := &Server{cfg: cfg, svc: cfg.Service, log: cfg.Log, ui: ui, api: http.NewServeMux(), guard: newLoginGuard()}
	s.guard.OnBlock = func(ip string, fails int, until time.Time) {
		s.log.Warn("web sign-in blocked for an address", "ip", ip, "fails", fails, "until", until.Format(time.RFC3339))
		if s.svc != nil {
			s.svc.LoginBlocked(context.Background(), ip, fails, until)
		}
	}
	s.routes()
	return s
}

// Serve listens on addr until ctx ends.
func (s *Server) Serve(ctx context.Context, addr string) error {
	srv := &http.Server{Addr: addr, Handler: s, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: time.Minute}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	s.log.Info("web panel listening", "addr", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// ServeHTTP serves the UI and the API under the secret path, or under a session's own path
// (random per login link, docs/STEALTH.md §1). Everything else is a plain 404 — or the decoy
// site when sessions have their own paths: then nginx sends the whole domain here.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	fixed, err := s.svc.WebPath(ctx)
	if err != nil || fixed == "" {
		http.NotFound(w, r)
		return
	}
	base, own := fixed, ""
	if !strings.HasPrefix(r.URL.Path+"/", fixed) {
		if p := firstSegment(r.URL.Path); p != "" && s.svc.WebPathKnown(ctx, p) {
			base, own = p, p
		} else {
			// The page of a session that has ended still calls its API: it is told to sign in
			// again (the decoy's 404 would read as a bare "Error"). Only the panel's own requests
			// (with the X-Vynel header) on a path a session used today get this.
			rest := strings.TrimPrefix(r.URL.Path, p)
			if p != "" && strings.HasPrefix(rest, "api/") && r.Header.Get(csrfHeader) == "1" {
				if why := s.svc.WebPathEnded(ctx, p); why != "" {
					writeError(w, &httpError{http.StatusUnauthorized, why})
					return
				}
			}
			s.notFound(w, r)
			return
		}
	}
	if r.URL.Path == strings.TrimSuffix(base, "/") {
		http.Redirect(w, r, base, http.StatusFound)
		return
	}
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Robots-Tag", "noindex, nofollow")
	// Only Telegram may show the panel in a frame: Telegram Web opens Mini Apps in an iframe.
	h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; frame-ancestors https://web.telegram.org; base-uri 'none'; form-action 'self'")

	rest := strings.TrimPrefix(r.URL.Path, base)
	if rest == "api" || strings.HasPrefix(rest, "api/") {
		h.Set("Cache-Control", "no-store")
		r2 := r.Clone(ctx)
		r2.URL.Path = "/" + rest
		r2 = r2.WithContext(context.WithValue(context.WithValue(r2.Context(), baseKey{}, base), ownPathKey{}, own))
		s.api.ServeHTTP(w, r2)
		return
	}
	s.static(w, r, rest)
}

// notFound answers a path outside the panel: the decoy site when nginx sends the whole domain
// here (session paths), a plain 404 otherwise.
func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !s.svc.SessionPaths(ctx) {
		http.NotFound(w, r)
		return
	}
	name, _ := s.svc.Setting(ctx, service.SettingWebDecoy, "")
	if name == "" {
		name, _ = s.svc.Setting(ctx, service.SettingSubDecoy, "")
	}
	decoy.Serve(w, r, name)
}

// firstSegment returns "/x/" for "/x/..." ("" when there is none).
func firstSegment(p string) string {
	rest, ok := strings.CutPrefix(p, "/")
	if !ok {
		return ""
	}
	seg, _, _ := strings.Cut(rest, "/")
	if seg == "" {
		return ""
	}
	return "/" + seg + "/"
}

type baseKey struct{}

// ownPathKey holds the session path of the request ("" under the panel's fixed path).
type ownPathKey struct{}

func ownPath(r *http.Request) string {
	p, _ := r.Context().Value(ownPathKey{}).(string)
	return p
}

func (s *Server) static(w http.ResponseWriter, r *http.Request, rest string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := path.Clean("/" + rest)[1:]
	if name == "" {
		name = "index.html"
	}
	b, err := fs.ReadFile(s.ui, name)
	if err != nil {
		// Unknown paths get the app; it routes by the URL hash.
		name = "index.html"
		if b, err = fs.ReadFile(s.ui, name); err != nil {
			http.Error(w, "the web UI is not built into this binary (make web)", http.StatusNotFound)
			return
		}
	}
	if name == "index.html" {
		w.Header().Set("Cache-Control", "no-store")
	} else if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(b))
}

// ---- sessions ----

type session struct {
	Login string `json:"l"`
	Exp   int64  `json:"e"`
	ID    string `json:"s"` // server-side session (store.WebSession); cookies without one are refused
}

func (s *Server) sign(ctx context.Context, sess session) (string, error) {
	key, err := s.svc.SessionKey(ctx)
	if err != nil {
		return "", err
	}
	payload, _ := json.Marshal(sess)
	p := base64.RawURLEncoding.EncodeToString(payload)
	m := hmac.New(sha256.New, key)
	m.Write([]byte(p))
	return p + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil)), nil
}

func (s *Server) verify(r *http.Request, v string) (*session, bool) {
	sess, why := s.check(r, v, false)
	return sess, why == ""
}

// check verifies a session cookie. active: the request is something the admin did (it resets
// the inactivity timer). It returns why the session cannot be used ("" = it can): "bad" for a
// forged or stale cookie, else a service.End* reason.
func (s *Server) check(r *http.Request, v string, active bool) (*session, string) {
	ctx := r.Context()
	p, sig, ok := strings.Cut(v, ".")
	if !ok {
		return nil, "bad"
	}
	key, err := s.svc.SessionKey(ctx)
	if err != nil {
		return nil, "bad"
	}
	m := hmac.New(sha256.New, key)
	m.Write([]byte(p))
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(got, m.Sum(nil)) {
		return nil, "bad"
	}
	raw, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil {
		return nil, "bad"
	}
	var sess session
	if json.Unmarshal(raw, &sess) != nil || time.Now().Unix() > sess.Exp {
		return nil, "bad"
	}
	login, _ := s.svc.Setting(ctx, service.SettingWebLogin, "")
	if sess.Login != login || sess.ID == "" {
		return nil, "bad"
	}
	// A session started from a link with its own path works under that path only.
	if s.svc.WebSessionPath(ctx, sess.ID) != ownPath(r) {
		return nil, "bad"
	}
	if why := s.svc.WebSessionCheck(ctx, sess.ID, clientIP(r), active); why != "" {
		return nil, why
	}
	return &sess, ""
}

func secure(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

// startSession signs a browser in: it records the session (the bot hears about it) and sets the
// cookie.
func (s *Server) startSession(w http.ResponseWriter, r *http.Request, method, who string) (string, error) {
	login, _ := s.svc.Setting(r.Context(), service.SettingWebLogin, "")
	if login == "" {
		login = "admin"
	}
	ws, err := s.svc.StartWebSession(r.Context(), method, who, clientIP(r), r.UserAgent(), ownPath(r), sessionTTL)
	if err != nil {
		return "", err
	}
	return login, s.writeCookie(w, r, session{Login: login, Exp: ws.ExpiresAt, ID: ws.ID})
}

func (s *Server) writeCookie(w http.ResponseWriter, r *http.Request, sess session) error {
	v, err := s.sign(r.Context(), sess)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: v, Path: basePath(r), Expires: time.Unix(sess.Exp, 0), HttpOnly: true,
		Secure: secure(r), SameSite: http.SameSiteStrictMode})
	return nil
}

// current is the session of the request (already verified by handle).
func current(r *http.Request) session {
	var sess session
	if c, err := r.Cookie(cookieName); err == nil {
		if p, _, ok := strings.Cut(c.Value, "."); ok {
			if raw, err := base64.RawURLEncoding.DecodeString(p); err == nil {
				_ = json.Unmarshal(raw, &sess)
			}
		}
	}
	return sess
}

func basePath(r *http.Request) string {
	if b, ok := r.Context().Value(baseKey{}).(string); ok {
		return b
	}
	return "/"
}

// ---- handler plumbing ----

type apiFunc func(r *http.Request) (any, error)

type httpError struct {
	code int
	msg  string
}

func (e *httpError) Error() string { return e.msg }

func badRequest(msg string) error { return &httpError{http.StatusBadRequest, msg} }

// handle registers an authenticated endpoint. Mutating requests must carry the X-Vynel header,
// which a cross-site form cannot send (CSRF), on top of the SameSite=Strict cookie.
func (s *Server) handle(pattern string, fn apiFunc) {
	s.api.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(cookieName)
		if err != nil {
			writeError(w, &httpError{http.StatusUnauthorized, "not logged in"})
			return
		}
		// Background requests (GET, the page's ping) keep the session alive but are not actions.
		active := r.Method != http.MethodGet && r.Method != http.MethodHead && !strings.HasSuffix(r.URL.Path, "/api/session/ping")
		if _, why := s.check(r, c.Value, active); why != "" {
			msg := "session expired"
			if why != "bad" {
				msg = service.EndReasonText(why, s.svc.WebIdleMinutes(r.Context()))
			}
			writeError(w, &httpError{http.StatusUnauthorized, msg})
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get(csrfHeader) != "1" {
			writeError(w, &httpError{http.StatusForbidden, "missing " + csrfHeader + " header"})
			return
		}
		s.respond(w, r.WithContext(context.WithValue(r.Context(), writerKey{}, w)), fn)
	})
}

func (s *Server) respond(w http.ResponseWriter, r *http.Request, fn apiFunc) {
	v, err := fn(r)
	if err != nil {
		var he *httpError
		if !errors.As(err, &he) {
			switch {
			case errors.Is(err, service.ErrNotFound):
				he = &httpError{http.StatusNotFound, "не найдено"}
			case errors.Is(err, service.ErrConflict):
				he = &httpError{http.StatusConflict, "такое имя уже занято"}
			case errors.Is(err, service.ErrInvalid):
				he = &httpError{http.StatusBadRequest, strings.TrimPrefix(err.Error(), service.ErrInvalid.Error()+": ")}
			default:
				s.log.Error("web api", "path", r.URL.Path, "err", err)
				he = &httpError{http.StatusInternalServerError, err.Error()}
			}
		}
		writeError(w, he)
		return
	}
	if raw, ok := v.(rawResponse); ok {
		w.Header().Set("Content-Type", raw.contentType)
		_, _ = w.Write(raw.body)
		return
	}
	if v == nil {
		v = map[string]bool{"ok": true}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

type rawResponse struct {
	contentType string
	body        []byte
}

func writeError(w http.ResponseWriter, e *httpError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(e.code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": e.msg})
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		return badRequest("bad JSON: " + err.Error())
	}
	return nil
}

func pathID(r *http.Request, name string) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil {
		return 0, badRequest("bad id")
	}
	return id, nil
}

func actor(r *http.Request) service.Actor {
	return service.Actor{Kind: "admin", ID: current(r).Login}
}

func clientIP(r *http.Request) string { return clientip.From(r) }

// ---- login ----

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in struct{ Login, Password string }
	if r.Header.Get(csrfHeader) != "1" {
		writeError(w, &httpError{http.StatusForbidden, "missing " + csrfHeader + " header"})
		return
	}
	if err := decode(r, &in); err != nil {
		writeError(w, &httpError{http.StatusBadRequest, err.Error()})
		return
	}
	if !s.svc.PasswordLoginEnabled(r.Context()) || ownPath(r) != "" {
		writeError(w, &httpError{http.StatusForbidden, "вход по паролю выключен — войдите по одноразовой ссылке (/login в боте или vynel admin login-link)"})
		return
	}
	done, err := s.guard.begin(clientIP(r))
	if err != nil {
		writeLoginError(w, err)
		return
	}
	err = s.svc.CheckAdmin(r.Context(), strings.TrimSpace(in.Login), in.Password)
	done(err == nil)
	if err != nil {
		time.Sleep(loginFailDelay)
		s.log.Warn("web login failed", "ip", clientIP(r), "login", in.Login)
		writeError(w, &httpError{http.StatusUnauthorized, "неверный логин или пароль"})
		return
	}
	login, err := s.startSession(w, r, service.LoginPassword, strings.TrimSpace(in.Login))
	if err != nil {
		writeError(w, &httpError{http.StatusInternalServerError, err.Error()})
		return
	}
	s.log.Info("web login", "ip", clientIP(r), "login", login)
	s.respond(w, r, func(*http.Request) (any, error) { return map[string]string{"login": login}, nil })
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		if sess, ok := s.verify(r, c.Value); ok {
			_ = s.svc.Logout(r.Context(), actor(r), sess.ID)
		}
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: basePath(r), MaxAge: -1, HttpOnly: true, Secure: secure(r), SameSite: http.SameSiteStrictMode})
	s.respond(w, r, func(*http.Request) (any, error) { return nil, nil })
}
