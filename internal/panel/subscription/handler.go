package subscription

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/vyto4ka/vynel/internal/decoy"
	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
)

// rules map User-Agents to formats (docs/ARCHITECTURE.md §8.3); the first match wins.
var rules = []struct {
	re     *regexp.Regexp
	format Format
}{
	{regexp.MustCompile(`(?i)keqdroid|keqdis`), FormatBase64}, // reads vless:// links best (it also takes Clash)
	{regexp.MustCompile(`(?i)clash|mihomo|stash|flclash|koala`), FormatMihomo},
	{regexp.MustCompile(`(?i)sing-?box|\bSF[AIMT]\b|karing`), FormatSingBox},
	{regexp.MustCompile(`(?i)happ|v2raytun|v2rayn|v2rayng|streisand|hiddify|incy|shadowrocket|nekobox|nekoray|v2box|foxray`), FormatBase64},
}

// UARule is a User-Agent rule as the UI shows it.
type UARule struct {
	Pattern string `json:"pattern"`
	Format  string `json:"format"`
}

// UARules lists the User-Agent rules in order.
func UARules() []UARule {
	out := make([]UARule, 0, len(rules))
	for _, r := range rules {
		out = append(out, UARule{Pattern: r.re.String(), Format: string(r.format)})
	}
	return out
}

// Detect picks the format: explicit URL suffix, then the user's client type, then UA rules,
// then HTML for browsers, else base64.
func Detect(r *http.Request, explicit, clientType string) Format {
	if f, ok := ParseFormat(explicit); ok {
		return f
	}
	if f, ok := ParseFormat(clientType); ok {
		return f
	}
	ua := r.UserAgent()
	for _, rule := range rules {
		if rule.re.MatchString(ua) {
			return rule.format
		}
	}
	if strings.Contains(r.Header.Get("Accept"), "text/html") && strings.Contains(ua, "Mozilla") {
		return FormatHTML
	}
	return FormatBase64
}

// Handler serves subscriptions. Everything that is not a valid token gets the decoy site's
// 404 page, so the endpoint cannot be told apart from a plain website (docs/STEALTH.md §2.4).
type Handler struct {
	svc *service.Service
	log *slog.Logger

	MaxMisses int           // unknown tokens per IP per minute before a ban
	BanFor    time.Duration // ban duration

	mu     sync.Mutex
	misses map[string]*missCounter
}

type missCounter struct {
	n           int
	windowStart time.Time
	bannedUntil time.Time
}

// NewHandler creates the subscription handler.
func NewHandler(svc *service.Service, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{svc: svc, log: log, MaxMisses: 20, BanFor: time.Hour, misses: map[string]*missCounter{}}
}

// Serve listens on addr until ctx ends.
func (h *Handler) Serve(ctx context.Context, addr string) error {
	srv := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: time.Minute}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	h.log.Info("subscriptions listening", "addr", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ip := clientIP(r)
	if h.banned(ip) {
		h.notFound(ctx, w)
		return
	}
	prefix, _ := h.svc.Setting(ctx, service.SettingSubPrefix, service.DefaultSubPrefix)
	if r.Method != http.MethodGet && r.Method != http.MethodHead || !strings.HasPrefix(r.URL.Path, prefix) {
		h.miss(ip)
		h.notFound(ctx, w)
		return
	}
	token, explicit, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, prefix), "/")
	u, err := h.svc.UserBySubToken(ctx, token)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			h.log.Error("subscription lookup", "err", err)
		}
		h.miss(ip)
		h.notFound(ctx, w)
		return
	}
	format := Detect(r, explicit, u.ClientType)
	if err := h.svc.TouchSubscription(ctx, u.ID, r.UserAgent()); err != nil {
		h.log.Warn("touch subscription", "err", err)
	}
	w.Header().Set("Cache-Control", "no-store")
	for _, hv := range h.headersFor(ctx, u, r.UserAgent()) {
		w.Header().Set(hv.Name, hv.Value)
	}
	if format == FormatHTML {
		h.page(ctx, w, r, u)
		return
	}

	hosts, reason := h.hostsFor(ctx, r, u, ip, format)
	uuid := u.UUID
	if reason != "" {
		// A refused client gets no credentials at all, only a server named after the reason.
		hosts, uuid = []service.Host{Stub("⛔ " + reason)}, StubUUID
	}
	var body []byte
	switch format {
	case FormatMihomo:
		body, err = Mihomo(hosts, uuid)
		w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	case FormatSingBox:
		body, err = SingBox(hosts, uuid)
		w.Header().Set("Content-Type", "application/json")
	case FormatXray:
		body, err = XrayJSON(hosts, uuid)
		w.Header().Set("Content-Type", "application/json")
	default:
		body = Base64(hosts, uuid)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	if err != nil {
		h.log.Error("render subscription", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(body)
}

// hostsFor returns the user's hosts for a format, or a reason why the user gets a stub.
func (h *Handler) hostsFor(ctx context.Context, r *http.Request, u *store.User, ip string, format Format) ([]service.Host, string) {
	switch u.Status {
	case store.StatusExpired:
		exp := ""
		if u.ExpireAt != nil {
			exp = " " + time.Unix(*u.ExpireAt, 0).Format("02.01.2006")
		}
		return nil, "Подписка истекла" + exp
	case store.StatusLimited:
		return nil, "Трафик исчерпан"
	case store.StatusDisabled:
		return nil, "Подписка отключена"
	}
	v, err := h.svc.CheckDevice(ctx, u, service.DeviceInfo{
		HWID: r.Header.Get("x-hwid"), Platform: r.Header.Get("x-device-os"), OSVersion: r.Header.Get("x-ver-os"),
		Model: r.Header.Get("x-device-model"), UserAgent: r.UserAgent(), IP: ip,
	})
	if err != nil {
		h.log.Error("hwid check", "err", err)
		return nil, "Временная ошибка, обновите подписку позже"
	}
	if !v.OK {
		return nil, v.Reason
	}
	all, err := h.svc.UserHosts(ctx, u.ID)
	if err != nil {
		h.log.Error("user hosts", "err", err)
		return nil, "Временная ошибка, обновите подписку позже"
	}
	var out []service.Host
	for _, host := range all {
		if !host.Hidden && format.Supports(host) {
			out = append(out, host)
		}
	}
	if len(out) == 0 {
		return nil, "Нет серверов для этого приложения"
	}
	return out, ""
}

func (h *Handler) notFound(ctx context.Context, w http.ResponseWriter) {
	name, _ := h.svc.Setting(ctx, service.SettingSubDecoy, "docs")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write(decoy.NotFound(name))
}

func (h *Handler) banned(ip string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	c := h.misses[ip]
	return c != nil && time.Now().Before(c.bannedUntil)
}

func (h *Handler) miss(ip string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	c := h.misses[ip]
	if c == nil || now.Sub(c.windowStart) > time.Minute {
		if len(h.misses) > 100000 { // crude memory bound
			h.misses = map[string]*missCounter{}
		}
		c = &missCounter{windowStart: now}
		h.misses[ip] = c
	}
	c.n++
	if c.n > h.MaxMisses && now.After(c.bannedUntil) {
		c.bannedUntil = now.Add(h.BanFor)
		h.log.Warn("banning IP probing subscription tokens", "ip", ip)
	}
}

// clientIP trusts X-Forwarded-For only from the local reverse proxy (Caddy).
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
