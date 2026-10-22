package subscription

import (
	"context"
	"errors"
	"fmt"
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

// singBoxCore matches apps built on sing-box: they have no VLESS XHTTP, whatever format they get.
var singBoxCore = regexp.MustCompile(`(?i)karing|sing-?box|\bSF[AIMT]\b`)

// ruleRes caches compiled rule patterns; rules change rarely and are few.
var ruleRes sync.Map // pattern → *regexp.Regexp (nil when invalid)

func ruleRe(p string) *regexp.Regexp {
	if v, ok := ruleRes.Load(p); ok {
		re, _ := v.(*regexp.Regexp)
		return re
	}
	re, err := regexp.Compile(p)
	if err != nil {
		re = nil
	}
	ruleRes.Store(p, re)
	return re
}

// Detect picks the format: explicit URL suffix, then the user's client type, then the
// User-Agent rules (first enabled match), then HTML for browsers, else base64.
func Detect(r *http.Request, explicit, clientType string, rules []service.SubRule) Format {
	f, _ := Pick(r, explicit, clientType, rules)
	return f
}

// Pick is Detect that also says which client rule matched (nil = none): its own template, if
// any, is what the servers go into when the format is the rule's.
func Pick(r *http.Request, explicit, clientType string, rules []service.SubRule) (Format, *service.SubRule) {
	ua := r.UserAgent()
	var matched *service.SubRule
	for i := range rules {
		if !rules[i].Enabled {
			continue
		}
		if re := ruleRe(rules[i].Pattern); re != nil && re.MatchString(ua) {
			if _, ok := ParseFormat(rules[i].Format); ok {
				matched = &rules[i]
				break
			}
		}
	}
	if f, ok := ParseFormat(explicit); ok {
		return f, matched
	}
	if f, ok := ParseFormat(clientType); ok {
		return f, matched
	}
	if matched != nil {
		f, _ := ParseFormat(matched.Format)
		return f, matched
	}
	if strings.Contains(r.Header.Get("Accept"), "text/html") && strings.Contains(ua, "Mozilla") {
		return FormatHTML, nil
	}
	return FormatBase64, nil
}

// templateFor is the client's own template for the format ("" = none).
func templateFor(f Format, rule *service.SubRule) string {
	if rule == nil || rule.Template == "" {
		return ""
	}
	if rf, _ := ParseFormat(rule.Format); rf != f {
		return ""
	}
	return rule.Template
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
	prefix := h.svc.SubPrefix(ctx)
	if r.Method != http.MethodGet && r.Method != http.MethodHead || !strings.HasPrefix(r.URL.Path, prefix) {
		h.miss(ip)
		h.notFound(ctx, w)
		return
	}
	token, explicit, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, prefix), "/")
	// The ban stops guessing short own links. A long token cannot be guessed, so it works from a
	// banned address too: one address may be many people (a mobile operator's NAT).
	if len(token) < unguessable && h.banned(ip) {
		h.notFound(ctx, w)
		return
	}
	u, err := h.svc.UserBySubToken(ctx, token)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			h.log.Error("subscription lookup", "err", err)
		}
		h.miss(ip)
		h.notFound(ctx, w)
		return
	}
	rules, err := h.svc.SubRules(ctx)
	if err != nil {
		h.log.Warn("subscription rules", "err", err)
	}
	format, rule := Pick(r, explicit, u.ClientType, rules)
	tpl := templateFor(format, rule)
	if format == FormatHTML {
		if on, _ := h.svc.Setting(ctx, service.SettingSubPageEnabled, "true"); on == "false" {
			// Without the page a browser cannot tell the link from any other missing page.
			h.notFound(ctx, w)
			return
		}
	}
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

	hosts, reason := h.hostsFor(ctx, w, r, u, ip, format)
	uuid := u.UUID
	if reason != "" {
		// A refused client gets no credentials at all, only a server named after the reason.
		hosts, uuid = []service.Host{Stub("⛔ " + reason)}, StubUUID
	}
	routing, err := h.svc.ClientRouting(ctx)
	if err != nil {
		h.log.Warn("subscription routing", "err", err)
	}
	var body []byte
	switch {
	case format == FormatMihomo && tpl != "":
		body, err = MihomoFromTemplate(hosts, uuid, tpl)
		w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	case format == FormatMihomo:
		body, err = Mihomo(hosts, uuid, routing)
		w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	case format == FormatSingBox && tpl != "":
		body, err = SingBoxFromTemplate(hosts, uuid, tpl)
		w.Header().Set("Content-Type", "application/json")
	case format == FormatSingBox:
		body, err = SingBox(hosts, uuid, routing)
		w.Header().Set("Content-Type", "application/json")
	case format == FormatXray && tpl != "":
		body, err = XrayJSONFromTemplate(hosts, uuid, tpl)
		w.Header().Set("Content-Type", "application/json")
	case format == FormatXray:
		body, err = XrayJSON(hosts, uuid, routing)
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
func (h *Handler) hostsFor(ctx context.Context, w http.ResponseWriter, r *http.Request, u *store.User, ip string, format Format) ([]service.Host, string) {
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
	// An address over the user's limit of addresses at once: it got the servers once, then the
	// nodes reported it and blocked it. The same header as the HWID limit tells the app why.
	if blocked, count, limit := h.svc.IPs().Verdict(u.ID, ip); blocked {
		w.Header().Set("x-hwid-max-devices-reached", "true")
		return nil, fmt.Sprintf("Превышен лимит подключений: %d/%d — отключите лишние устройства", count, limit)
	}
	v, err := h.svc.CheckDevice(ctx, u, service.DeviceInfo{
		HWID: r.Header.Get("x-hwid"), Platform: r.Header.Get("x-device-os"), OSVersion: r.Header.Get("x-ver-os"),
		Model: r.Header.Get("x-device-model"), UserAgent: r.UserAgent(), IP: ip,
	})
	if err != nil {
		h.log.Error("hwid check", "err", err)
		return nil, "Временная ошибка, обновите подписку позже"
	}
	// The same response headers as Remnawave: clients such as Happ and Karing use them to
	// turn on sending x-hwid by themselves.
	if !v.Off {
		w.Header().Set("x-hwid-active", "true")
		if v.NoHWID {
			w.Header().Set("x-hwid-not-supported", "true")
		}
		if v.Full {
			w.Header().Set("x-hwid-max-devices-reached", "true")
		}
	}
	if !v.OK {
		return nil, v.Reason
	}
	// A panel merging this subscription in says how deep it is (see DepthHeader).
	all, err := h.svc.SubscriptionHosts(ctx, u, service.RequestDepth(r.Header.Get(service.DepthHeader)))
	if err != nil {
		h.log.Error("user hosts", "err", err)
		return nil, "Временная ошибка, обновите подписку позже"
	}
	noXHTTP := singBoxCore.MatchString(r.UserAgent())
	var out []service.Host
	for _, host := range all {
		if !host.Hidden && format.Supports(host) && (!noXHTTP || host.Network != "xhttp") {
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

// unguessable is the token length from which guessing is hopeless (random links have 43).
const unguessable = 32

func (h *Handler) miss(ip string) {
	// Without the client's address (an old Reality front without PROXY, a local proxy) every
	// visitor looks like 127.0.0.1: banning it would take subscriptions down for everyone. The
	// same goes for our nodes' addresses: every app updating through the tunnel comes from there,
	// and old links of all of them add up.
	if p := net.ParseIP(ip); p == nil || p.IsLoopback() || h.svc != nil && h.svc.IPs().Ours(ip) {
		return
	}
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
