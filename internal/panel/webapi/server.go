// Package webapi serves the admin web UI and its JSON API under the secret path
// (docs/STEALTH.md §2.2). It is a thin transport over internal/panel/service.
package webapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/web"
)

// Config configures the web server.
type Config struct {
	Service       *service.Service
	CAFingerprint func() string    // for node join tokens
	Connected     func(int64) bool // live node sessions; nil = guess from last_seen
	Version       string
	Log           *slog.Logger
	UI            fs.FS // built UI; nil = web.Dist
}

// Server is the web panel.
type Server struct {
	cfg     Config
	svc     *service.Service
	log     *slog.Logger
	ui      fs.FS
	api     *http.ServeMux
	loginMu sync.Mutex // one password check at a time: slows down guessing
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
	s := &Server{cfg: cfg, svc: cfg.Service, log: cfg.Log, ui: ui, api: http.NewServeMux()}
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

// ServeHTTP serves the UI and the API under the secret path; everything else is a plain 404.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	base, err := s.svc.WebPath(r.Context())
	if err != nil || base == "" {
		http.NotFound(w, r)
		return
	}
	if r.URL.Path == strings.TrimSuffix(base, "/") {
		http.Redirect(w, r, base, http.StatusFound)
		return
	}
	if !strings.HasPrefix(r.URL.Path, base) {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("X-Frame-Options", "DENY")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Robots-Tag", "noindex, nofollow")
	h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")

	rest := strings.TrimPrefix(r.URL.Path, base)
	if rest == "api" || strings.HasPrefix(rest, "api/") {
		h.Set("Cache-Control", "no-store")
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/" + rest
		r2 = r2.WithContext(context.WithValue(r2.Context(), baseKey{}, base))
		s.api.ServeHTTP(w, r2)
		return
	}
	s.static(w, r, rest)
}

type baseKey struct{}

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
	http.ServeContent(w, r, name, time.Time{}, strings.NewReader(string(b)))
}

// ---- sessions ----

type session struct {
	Login string `json:"l"`
	Exp   int64  `json:"e"`
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

func (s *Server) verify(ctx context.Context, v string) (*session, bool) {
	p, sig, ok := strings.Cut(v, ".")
	if !ok {
		return nil, false
	}
	key, err := s.svc.SessionKey(ctx)
	if err != nil {
		return nil, false
	}
	m := hmac.New(sha256.New, key)
	m.Write([]byte(p))
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(got, m.Sum(nil)) {
		return nil, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil {
		return nil, false
	}
	var sess session
	if json.Unmarshal(raw, &sess) != nil || time.Now().Unix() > sess.Exp {
		return nil, false
	}
	login, _ := s.svc.Setting(ctx, service.SettingWebLogin, "")
	if sess.Login != login {
		return nil, false
	}
	return &sess, true
}

func secure(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

func (s *Server) setCookie(w http.ResponseWriter, r *http.Request, login string) error {
	exp := time.Now().Add(sessionTTL)
	v, err := s.sign(r.Context(), session{Login: login, Exp: exp.Unix()})
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: v, Path: basePath(r), Expires: exp, HttpOnly: true,
		Secure: secure(r), SameSite: http.SameSiteStrictMode})
	return nil
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
		if _, ok := s.verify(r.Context(), c.Value); !ok {
			writeError(w, &httpError{http.StatusUnauthorized, "session expired"})
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
	login := ""
	if c, err := r.Cookie(cookieName); err == nil {
		if p, _, ok := strings.Cut(c.Value, "."); ok {
			if raw, err := base64.RawURLEncoding.DecodeString(p); err == nil {
				var sess session
				_ = json.Unmarshal(raw, &sess)
				login = sess.Login
			}
		}
	}
	return service.Actor{Kind: "admin", ID: login}
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first, _, _ := strings.Cut(xff, ",")
			return strings.TrimSpace(first)
		}
	}
	return host
}

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
	s.loginMu.Lock()
	err := s.svc.CheckAdmin(r.Context(), strings.TrimSpace(in.Login), in.Password)
	if err != nil {
		time.Sleep(700 * time.Millisecond)
	}
	s.loginMu.Unlock()
	if err != nil {
		s.log.Warn("web login failed", "ip", clientIP(r), "login", in.Login)
		writeError(w, &httpError{http.StatusUnauthorized, "неверный логин или пароль"})
		return
	}
	login, _ := s.svc.Setting(r.Context(), service.SettingWebLogin, "")
	if err := s.setCookie(w, r, login); err != nil {
		writeError(w, &httpError{http.StatusInternalServerError, err.Error()})
		return
	}
	s.log.Info("web login", "ip", clientIP(r), "login", login)
	s.respond(w, r, func(*http.Request) (any, error) { return map[string]string{"login": login}, nil })
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: basePath(r), MaxAge: -1, HttpOnly: true, Secure: secure(r), SameSite: http.SameSiteStrictMode})
	s.respond(w, r, func(*http.Request) (any, error) { return nil, nil })
}
