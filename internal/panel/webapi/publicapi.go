package webapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vyto4ka/vynel/internal/i18n"
	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
)

// The public API (docs/API.md): JSON over HTTPS under <secret path>api/v1/, for other programs.
// It is not the panel's own API: it is authenticated by a token (Authorization: Bearer vyn_…)
// instead of a session cookie, its fields are stable and snake_case, and what a token may do is
// narrowed by scopes, networks and a rate limit.

// v1Route is one registered endpoint; the spec test compares the list with openapi.yaml.
type v1Route struct {
	Method string
	Path   string // relative to /api/v1
	Scope  string // "" = any valid token; "-" = no token
}

// v1Ctx is what a handler knows about the request.
type v1Ctx struct {
	tok   *store.APIToken
	lang  string
	reqID string
}

type v1Func func(r *http.Request, c *v1Ctx) (any, error)

// v1Result lets a handler pick the status code ("created", "no content").
type v1Result struct {
	status int
	body   any
}

func created(v any) v1Result { return v1Result{http.StatusCreated, v} }
func noContent() v1Result    { return v1Result{http.StatusNoContent, nil} }

// apiError is an error with a stable code. Codes are the contract: messages are for people.
type apiError struct {
	status     int
	code       string
	msg        string
	field      string
	retryAfter int
}

func (e *apiError) Error() string { return e.code + ": " + e.msg }

func apiErr(status int, code, msg string) *apiError {
	return &apiError{status: status, code: code, msg: msg}
}

func (e *apiError) on(field string) *apiError { c := *e; c.field = field; return &c }

func invalidField(field, msg string) *apiError {
	return &apiError{status: http.StatusUnprocessableEntity, code: "validation_failed", msg: msg, field: field}
}

func notFoundErr(what string) *apiError {
	return apiErr(http.StatusNotFound, what+"_not_found", what+" not found")
}

// v1State is the public API's memory: it is lost on restart, which only resets counters.
type v1State struct {
	routes []v1Route
	bans   failedAuth
	rates  rateWindows
	idem   [64]sync.Mutex // one request at a time per idempotency key
	now    func() time.Time
}

func (v *v1State) clock() time.Time {
	if v.now != nil {
		return v.now()
	}
	return time.Now()
}

// failedAuth counts wrong tokens per address; too many in a while block the address.
type failedAuth struct {
	mu sync.Mutex
	m  map[string]*authFails
}

type authFails struct {
	n     int
	since time.Time
	until time.Time
}

const (
	authFailLimit  = 10
	authFailWindow = 5 * time.Minute
	authBanFor     = 15 * time.Minute
)

// blocked says for how long an address is blocked (0 = it is not).
func (f *failedAuth) blocked(ip string, now time.Time) time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	if a := f.m[ip]; a != nil && now.Before(a.until) {
		return a.until.Sub(now)
	}
	return 0
}

func (f *failedAuth) fail(ip string, now time.Time) (banned bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.m == nil {
		f.m = map[string]*authFails{}
	}
	if len(f.m) > 10000 { // forget what has expired
		for k, a := range f.m {
			if now.After(a.until) && now.Sub(a.since) > authFailWindow {
				delete(f.m, k)
			}
		}
	}
	a := f.m[ip]
	if a == nil || now.Sub(a.since) > authFailWindow {
		a = &authFails{since: now}
		f.m[ip] = a
	}
	if a.n++; a.n >= authFailLimit {
		a.until, a.n = now.Add(authBanFor), 0
		return true
	}
	return false
}

func (f *failedAuth) ok(ip string) {
	f.mu.Lock()
	delete(f.m, ip)
	f.mu.Unlock()
}

// rateWindows counts requests per token in the current minute.
type rateWindows struct {
	mu sync.Mutex
	m  map[int64]*rateWindow
}

type rateWindow struct {
	start time.Time
	n     int
}

// take counts a request and says how many are left in the window and when it ends.
func (r *rateWindows) take(id int64, limit int, now time.Time) (left int, reset time.Time, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.m == nil {
		r.m = map[int64]*rateWindow{}
	}
	w := r.m[id]
	if w == nil || now.Sub(w.start) >= time.Minute {
		w = &rateWindow{start: now}
		r.m[id] = w
	}
	reset = w.start.Add(time.Minute)
	if w.n >= limit {
		return 0, reset, false
	}
	w.n++
	return limit - w.n, reset, true
}

// apiLang is the language of messages: English unless the client asks for another one.
func apiLang(r *http.Request) string {
	if r.Header.Get("X-Lang") == "" && r.Header.Get("Accept-Language") == "" {
		return "en"
	}
	return i18n.FromRequest(r)
}

func newRequestID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "req_" + hex.EncodeToString(b)
}

// v1 registers an endpoint of the public API. scope is what the token needs ("" = any token).
func (s *Server) v1(method, path, scope string, fn v1Func) {
	s.v1State.routes = append(s.v1State.routes, v1Route{method, path, scope})
	s.api.HandleFunc(method+" /api/v1"+path, func(w http.ResponseWriter, r *http.Request) { s.v1Serve(w, r, scope, fn) })
}

// v1Open registers an endpoint that needs no token (the spec).
func (s *Server) v1Open(method, path string, fn func(w http.ResponseWriter, r *http.Request)) {
	s.v1State.routes = append(s.v1State.routes, v1Route{method, path, "-"})
	s.api.HandleFunc(method+" /api/v1"+path, func(w http.ResponseWriter, r *http.Request) {
		if ownPath(r) != "" {
			s.v1Fail(w, r, &v1Ctx{lang: apiLang(r), reqID: newRequestID()}, notFoundErr("path"))
			return
		}
		fn(w, r)
	})
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

func (s *Server) v1Serve(w http.ResponseWriter, r *http.Request, scope string, fn v1Func) {
	c := &v1Ctx{lang: apiLang(r), reqID: newRequestID()}
	w.Header().Set("X-Request-Id", c.reqID)
	if ownPath(r) != "" { // a session's own path is the panel's, not the API's
		s.v1Fail(w, r, c, notFoundErr("path"))
		return
	}
	now, ip := s.v1State.clock(), clientIP(r)
	if d := s.v1State.bans.blocked(ip, now); d > 0 {
		e := apiErr(http.StatusTooManyRequests, "too_many_failures", "too many wrong tokens from this address, try later")
		e.retryAfter = int(d.Seconds()) + 1
		s.v1Fail(w, r, c, e)
		return
	}
	secret := bearer(r)
	if secret == "" {
		w.Header().Set("WWW-Authenticate", `Bearer realm="vynel"`)
		s.v1Fail(w, r, c, apiErr(http.StatusUnauthorized, "unauthenticated", "send the token as 'Authorization: Bearer vyn_…'"))
		return
	}
	tok, err := s.svc.AuthenticateAPIToken(r.Context(), secret, ip)
	if err != nil {
		var e *apiError
		switch {
		case errors.Is(err, service.ErrTokenExpired):
			e = apiErr(http.StatusUnauthorized, "token_expired", "the token has expired")
		case errors.Is(err, service.ErrTokenRevoked):
			e = apiErr(http.StatusUnauthorized, "token_revoked", "the token has been revoked")
		case errors.Is(err, service.ErrTokenIP):
			e = apiErr(http.StatusForbidden, "ip_not_allowed", "the token is not allowed from this address")
		case errors.Is(err, service.ErrTokenInvalid):
			e = apiErr(http.StatusUnauthorized, "invalid_token", "the token is not valid")
		default:
			s.v1Fail(w, r, c, err)
			return
		}
		// Only tokens that do not exist count towards a block: a revoked or expired one is a program
		// that was not told, and blocking its address would shut out the others behind it.
		if errors.Is(err, service.ErrTokenInvalid) && s.v1State.bans.fail(ip, now) {
			s.log.Warn("api: address blocked for wrong tokens", "ip", ip)
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="vynel", error="invalid_token"`)
		s.log.Warn("api: token refused", "ip", ip, "why", e.code)
		s.v1Fail(w, r, c, e)
		return
	}
	s.v1State.bans.ok(ip)
	c.tok = tok
	limit := tok.RateLimit
	if limit <= 0 {
		limit = service.DefaultAPIRate
	}
	left, reset, ok := s.v1State.rates.take(tok.ID, limit, now)
	w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limit))
	w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(left))
	w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10))
	if !ok {
		e := apiErr(http.StatusTooManyRequests, "rate_limited", "too many requests, slow down")
		e.retryAfter = int(reset.Sub(now).Seconds()) + 1
		s.v1Fail(w, r, c, e)
		return
	}
	if scope != "" && !service.HasScope(tok, scope) {
		s.v1Fail(w, r, c, apiErr(http.StatusForbidden, "insufficient_scope", "the token needs the '"+scope+"' permission"))
		return
	}

	// Requests that change something may carry an Idempotency-Key: a repeat gets the first answer.
	var body []byte
	if r.Body != nil && r.Method != http.MethodGet && r.Method != http.MethodHead {
		var err error
		if body, err = io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20)); err != nil {
			s.v1Fail(w, r, c, apiErr(http.StatusRequestEntityTooLarge, "body_too_large", "the request body is over 1 MiB"))
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
	}
	key := r.Header.Get("Idempotency-Key")
	if key != "" && r.Method != http.MethodGet && r.Method != http.MethodHead {
		if len(key) > 128 {
			s.v1Fail(w, r, c, apiErr(http.StatusBadRequest, "bad_idempotency_key", "Idempotency-Key is up to 128 characters"))
			return
		}
		h := sha256.Sum256(body)
		fp := r.Method + " " + r.URL.RequestURI() + " " + hex.EncodeToString(h[:])
		kh := sha256.Sum256([]byte(strconv.FormatInt(tok.ID, 10) + ":" + key))
		mu := &s.v1State.idem[int(kh[0])%len(s.v1State.idem)]
		mu.Lock()
		defer mu.Unlock()
		prev, err := s.svc.IdempotentReply(r.Context(), tok.ID, key)
		if err != nil {
			s.v1Fail(w, r, c, err)
			return
		}
		if prev != nil {
			if prev.Fingerprint != fp {
				s.v1Fail(w, r, c, apiErr(http.StatusUnprocessableEntity, "idempotency_key_reused", "this Idempotency-Key was used for a different request"))
				return
			}
			w.Header().Set("Idempotent-Replayed", "true")
			s.v1Write(w, prev.Status, []byte(prev.Response))
			return
		}
		status, out := s.v1Run(r, c, fn)
		if status < 500 && status != http.StatusTooManyRequests {
			_ = s.svc.SaveIdempotentReply(context.WithoutCancel(r.Context()), tok.ID, key, store.Idempotent{Fingerprint: fp, Status: status, Response: string(out)})
		}
		s.v1Write(w, status, out)
		return
	}
	status, out := s.v1Run(r, c, fn)
	s.v1Write(w, status, out)
}

// v1Run runs the handler and returns the status and the body to send.
func (s *Server) v1Run(r *http.Request, c *v1Ctx, fn v1Func) (int, []byte) {
	v, err := fn(r, c)
	if err != nil {
		e := s.v1Error(r, c, err)
		return e.status, errorBody(e, c)
	}
	status := http.StatusOK
	if res, ok := v.(v1Result); ok {
		status, v = res.status, res.body
	}
	if v == nil {
		if status == http.StatusOK {
			status = http.StatusNoContent
		}
		return status, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return http.StatusInternalServerError, errorBody(apiErr(http.StatusInternalServerError, "internal", "internal error"), c)
	}
	return status, b
}

func (s *Server) v1Write(w http.ResponseWriter, status int, body []byte) {
	if status == http.StatusNoContent || len(body) == 0 {
		w.WriteHeader(status)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body)
	_, _ = w.Write([]byte("\n"))
}

func errorBody(e *apiError, c *v1Ctx) []byte {
	m := map[string]any{"code": e.code, "message": e.msg, "request_id": c.reqID}
	if e.field != "" {
		m["field"] = e.field
	}
	b, _ := json.Marshal(map[string]any{"error": m})
	return b
}

// v1Fail answers an error right away.
func (s *Server) v1Fail(w http.ResponseWriter, r *http.Request, c *v1Ctx, err error) {
	e := s.v1Error(r, c, err)
	if e.retryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(e.retryAfter))
	}
	s.v1Write(w, e.status, errorBody(e, c))
}

// v1Error turns what a handler returned into an API error.
func (s *Server) v1Error(r *http.Request, c *v1Ctx, err error) *apiError {
	var ae *apiError
	var taken *service.IdentityTakenError
	var loc i18n.Localizer
	switch {
	case errors.As(err, &ae):
		return ae
	case errors.As(err, &taken):
		return &apiError{status: http.StatusConflict, code: taken.Field + "_taken", msg: taken.Field + " already belongs to another user", field: taken.Field}
	case errors.Is(err, service.ErrNotFound):
		return apiErr(http.StatusNotFound, "not_found", "not found")
	case errors.Is(err, service.ErrInvalid):
		msg := strings.TrimPrefix(err.Error(), service.ErrInvalid.Error()+": ")
		if errors.As(err, &loc) {
			msg = loc.Localize(c.lang)
		} else {
			msg = i18n.T(c.lang, msg)
		}
		return apiErr(http.StatusUnprocessableEntity, "validation_failed", msg)
	case errors.Is(err, service.ErrConflict):
		return apiErr(http.StatusConflict, "conflict", "the object already exists")
	}
	s.log.Error("api v1", "request", c.reqID, "method", r.Method, "path", r.URL.Path, "err", err)
	return apiErr(http.StatusInternalServerError, "internal", "internal error, quote the request_id when you ask for help")
}

// v1Catchall answers paths of the API that do not exist, as JSON.
func (s *Server) v1Catchall(w http.ResponseWriter, r *http.Request) {
	c := &v1Ctx{lang: apiLang(r), reqID: newRequestID()}
	w.Header().Set("X-Request-Id", c.reqID)
	if ownPath(r) == "" {
		rest := strings.TrimPrefix(r.URL.Path, "/api/v1")
		var allow []string
		for _, rt := range s.v1State.routes {
			if pathMatches(rt.Path, rest) {
				allow = append(allow, rt.Method)
			}
		}
		if len(allow) > 0 {
			w.Header().Set("Allow", strings.Join(allow, ", "))
			s.v1Fail(w, r, c, apiErr(http.StatusMethodNotAllowed, "method_not_allowed", "use "+strings.Join(allow, " or ")+" for this path"))
			return
		}
	}
	s.v1Fail(w, r, c, apiErr(http.StatusNotFound, "no_such_endpoint", "no such endpoint"))
}

// pathMatches compares a route pattern ("/users/{id}") with a path.
func pathMatches(pattern, path string) bool {
	a, b := strings.Split(strings.Trim(pattern, "/"), "/"), strings.Split(strings.Trim(path, "/"), "/")
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !(strings.HasPrefix(a[i], "{") || a[i] == b[i]) {
			return false
		}
	}
	return true
}

// ---- request helpers ----

// v1Decode reads a JSON body into v; unknown fields are an error, so a typo is not silently ignored.
func v1Decode(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return apiErr(http.StatusBadRequest, "bad_json", "a JSON body is required")
		}
		return apiErr(http.StatusBadRequest, "bad_json", "bad JSON: "+err.Error())
	}
	if dec.More() {
		return apiErr(http.StatusBadRequest, "bad_json", "bad JSON: extra data after the object")
	}
	return nil
}

// v1DecodeOptional is v1Decode for a body that may be absent.
func v1DecodeOptional(r *http.Request, v any) error {
	b, _ := io.ReadAll(r.Body)
	if len(bytes.TrimSpace(b)) == 0 {
		return nil
	}
	r.Body = io.NopCloser(bytes.NewReader(b))
	return v1Decode(r, v)
}

func v1ID(r *http.Request, name string) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		return 0, apiErr(http.StatusBadRequest, "bad_id", "the id in the path must be a positive number").on(name)
	}
	return id, nil
}

// v1Page reads limit and offset.
func v1Page(r *http.Request, def, max int) (limit, offset int, err error) {
	q := r.URL.Query()
	limit, offset = def, 0
	if v := q.Get("limit"); v != "" {
		if limit, err = strconv.Atoi(v); err != nil || limit < 1 || limit > max {
			return 0, 0, apiErr(http.StatusBadRequest, "bad_parameter", "limit is a number from 1 to "+strconv.Itoa(max)).on("limit")
		}
	}
	if v := q.Get("offset"); v != "" {
		if offset, err = strconv.Atoi(v); err != nil || offset < 0 {
			return 0, 0, apiErr(http.StatusBadRequest, "bad_parameter", "offset is a number, 0 or more").on("offset")
		}
	}
	return limit, offset, nil
}

func v1Actor(c *v1Ctx) service.Actor { return service.Actor{Kind: "api", ID: c.tok.Name} }

// rfc3339 formats unix seconds; nil stays null.
func rfc3339(t *int64) *string {
	if t == nil {
		return nil
	}
	s := time.Unix(*t, 0).UTC().Format(time.RFC3339)
	return &s
}

func rfc3339v(t int64) string { return time.Unix(t, 0).UTC().Format(time.RFC3339) }

func errorsIsConflict(err error) bool { return errors.Is(err, service.ErrConflict) }

func isNotFound(err error) bool { return errors.Is(err, service.ErrNotFound) }

func isIdentityTaken(err error) bool {
	var t *service.IdentityTakenError
	return errors.As(err, &t)
}
