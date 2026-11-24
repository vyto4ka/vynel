package subscription

import (
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/vyto4ka/vynel/internal/clientip"
	"github.com/vyto4ka/vynel/internal/decoy"
	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
)

// singBoxCore matches apps built on upstream sing-box (without XHTTP support).
// Karing uses its own sing-box-extended fork with VLESS XHTTP support.
var singBoxCore = regexp.MustCompile(`(?i)sing-?box|\bSF[AIMT]\b`)

var karingClient = regexp.MustCompile(`(?i)karing`)
var keqClient = regexp.MustCompile(`(?i)keqdroid|keqdis`)
var v2rayTunClient = regexp.MustCompile(`(?i)v2raytun`)

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
	srv := &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       time.Minute,
		MaxHeaderBytes:    1 << 20,
	}
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
	isGet := r.Method == http.MethodGet || r.Method == http.MethodHead
	if (!isGet && r.Method != http.MethodPost) || !strings.HasPrefix(r.URL.Path, prefix) {
		h.countMiss(ip, strings.TrimPrefix(r.URL.Path, "/"))
		h.notFound(ctx, w)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, prefix)
	token, explicit, _ := strings.Cut(rest, "/")
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
		h.countMiss(ip, rest)
		h.notFound(ctx, w)
		return
	}
	if r.Method == http.MethodPost {
		h.handleDeviceDelete(ctx, w, r, u, explicit)
		return
	}
	if explicit == "page.js" {
		if on, _ := h.svc.Setting(ctx, service.SettingSubPageEnabled, "true"); on == "false" {
			h.notFound(ctx, w)
			return
		}
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(PageScript)
		return
	}
	if name, ok := strings.CutPrefix(explicit, "karing-rules/"); ok {
		// Rule-set requests are metadata, not subscription/device registrations. No
		// credentials are served here, but the token and user status must still be valid.
		routing, err := h.svc.ClientRouting(ctx)
		body, found := KaringRuleSet(name, routing)
		if err != nil || !found || u.Status != store.StatusActive {
			h.notFound(ctx, w)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(body)
		return
	}
	if strings.HasPrefix(explicit, "geo/") {
		// Reserved for future client geo asset delivery (keeps decoy 404 anti-probing behavior).
		h.notFound(ctx, w)
		return
	}
	rules, err := h.svc.SubRules(ctx)
	if err != nil {
		h.log.Warn("subscription rules", "err", err)
	}
	format, rule := Pick(r, explicit, u.ClientType, rules)
	if format == FormatHTML {
		if on, _ := h.svc.Setting(ctx, service.SettingSubPageEnabled, "true"); on == "false" {
			// Without the page a browser cannot tell the link from any other missing page.
			h.notFound(ctx, w)
			return
		}
	}
	if err := h.svc.TouchSubscription(ctx, u.ID, clipUTF8(r.UserAgent(), 300)); err != nil {
		h.log.Warn("touch subscription", "err", err)
	}
	w.Header().Set("Cache-Control", "no-store")
	routing, err := h.svc.ClientRouting(ctx)
	if err != nil {
		h.log.Warn("subscription routing", "err", err)
	}
	if format == FormatHTML {
		for _, hv := range h.headersFor(ctx, u, r.UserAgent(), format, routing) {
			w.Header().Set(hv.Name, hv.Value)
		}
		h.page(ctx, w, r, u)
		return
	}

	selection, err := selectHosts(ctx, h.svc, u, r, format, mayUseLinks(explicit, u.ClientType), true)
	format = selection.Format
	hosts, reason := selection.Hosts, selection.Reason
	if err != nil {
		h.log.Error("subscription selection", "err", err)
		reason = "Временная ошибка, обновите подписку позже"
	}
	uuid := u.UUID
	if reason != "" {
		// A refused client gets no credentials at all, only a server named after the reason.
		hosts, uuid = []service.Host{Stub("⛔ " + reason)}, StubUUID
	}
	// Device policy may have changed the format to links.
	for _, hv := range h.headersFor(ctx, u, r.UserAgent(), format, routing) {
		w.Header().Set(hv.Name, hv.Value)
	}
	for _, header := range selection.Headers {
		w.Header().Set(header.Name, header.Value)
	}
	hosts = service.WithFragment(hosts, routing.Fragment)
	hs, _ := h.svc.SubHeaders(ctx)
	if LinkRouting(format, r.UserAgent(), hosts) {
		for _, hv := range RoutingHeaders(hs, nil, r.UserAgent(), h.svc.SubBasicsGet(ctx).Title, routing) {
			w.Header().Set(hv.Name, hv.Value)
		}
	}
	for _, hv := range FragmentHeaders(hs, nil, r.UserAgent(), format, routing) {
		w.Header().Set(hv.Name, hv.Value)
	}
	var subURL string
	if format == FormatKaring {
		subURL, err = h.svc.SubscriptionURL(ctx, u)
		if err != nil {
			h.log.Error("render subscription", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}
	body, contentType, _, err := RenderBody(format, hosts, "", uuid, r.UserAgent(), subURL, rule, routing)
	if err != nil {
		h.log.Error("render subscription", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", contentType)
	_, _ = w.Write(body)
}

// RenderBody renders the subscription payload for the given format, hosts, optional refusal reason,
// user UUID, User-Agent, subscription URL, matched rule, and routing config.
// For FormatHTML it returns an empty body (HTML previews are served via iframe).
// For FormatBase64 it also returns decodedUris containing the newline-separated URI list.
func RenderBody(format Format, hosts []service.Host, reason, uuid, ua, subURL string, rule *service.SubRule, routing service.ClientRouting) (body []byte, contentType string, decodedUris string, err error) {
	if format == FormatHTML {
		return nil, "text/html; charset=utf-8", "", nil
	}
	tpl := templateFor(format, rule)
	if format == FormatBase64 {
		tpl = ""
	}
	if reason != "" {
		// A refused client gets no credentials at all, only a server named after the reason.
		hosts, uuid = []service.Host{Stub("⛔ " + reason)}, StubUUID
	}
	hosts = service.WithFragment(hosts, routing.Fragment)
	switch {
	case format == FormatKaring:
		body, err = Karing(hosts, uuid, subURL, routing)
		contentType = "application/json"
	case format == FormatXray && keqClient.MatchString(ua) && hasRawHosts(hosts):
		// KeqDroid accepts full configs and URI links in one text payload. Keeping the
		// links avoids dropping external WS/gRPC, VMess, Trojan, SS, etc. servers.
		body, err = KeqMixed(hosts, uuid, routing, tpl)
		contentType = "text/plain; charset=utf-8"
	case format == FormatMihomo && tpl != "":
		body, err = MihomoFromTemplate(hosts, uuid, tpl)
		contentType = "text/yaml; charset=utf-8"
	case format == FormatMihomo:
		body, err = Mihomo(hosts, uuid, routing)
		contentType = "text/yaml; charset=utf-8"
	case format == FormatSingBox && tpl != "":
		body, err = SingBoxFromTemplate(hosts, uuid, tpl)
		contentType = "application/json"
	case format == FormatSingBox:
		body, err = SingBox(hosts, uuid, routing)
		contentType = "application/json"
	case format == FormatXray && tpl != "":
		body, err = XrayJSONFromTemplate(hosts, uuid, tpl)
		contentType = "application/json"
	case format == FormatXray:
		body, err = XrayJSON(hosts, uuid, routing)
		contentType = "application/json"
	default:
		lines := make([]string, 0, len(hosts))
		for _, h := range hosts {
			lines = append(lines, linkForUA(h, uuid, ua))
		}
		decodedUris = strings.Join(lines, "\n")
		body = []byte(base64.StdEncoding.EncodeToString([]byte(decodedUris)))
		contentType = "text/plain; charset=utf-8"
	}
	return body, contentType, decodedUris, err
}

func (h *Handler) notFound(ctx context.Context, w http.ResponseWriter) {
	name, _ := h.svc.Setting(ctx, service.SettingSubDecoy, "docs")
	decoy.SetSecurityHeaders(w)
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

// countMiss registers a failed token attempt, but only when the path looks like one: a single
// segment without a dot. Scanner and browser noise (/robots.txt, /favicon.ico,
// /assets/app.js, /.well-known/…) must not burn the miss budget: one busy crawler would
// otherwise ban a carrier NAT shared with real users. With links at the domain root every hit
// reaches the handler, so the discrimination matters even more.
func (h *Handler) countMiss(ip, path string) {
	if path == "" || strings.ContainsAny(path, "/.") {
		return
	}
	h.miss(ip)
}

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
	if c == nil {
		if len(h.misses) >= 100000 {
			for k, m := range h.misses {
				if now.After(m.bannedUntil) && now.Sub(m.windowStart) > time.Minute {
					delete(h.misses, k)
				}
			}
			if len(h.misses) >= 100000 {
				return
			}
		}
		c = &missCounter{windowStart: now}
		h.misses[ip] = c
	} else if now.Sub(c.windowStart) > time.Minute {
		c.n, c.windowStart = 0, now
	}
	c.n++
	if c.n > h.MaxMisses && now.After(c.bannedUntil) {
		c.bannedUntil = now.Add(h.BanFor)
		h.log.Warn("banning IP probing subscription tokens", "ip", ip)
	}
}

// clientIP trusts X-Forwarded-For only from the local reverse proxy.
func clientIP(r *http.Request) string { return clientip.From(r) }
