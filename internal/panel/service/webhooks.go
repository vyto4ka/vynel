package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/store"
)

// Webhooks push API events to an address. A delivery is queued in the same transaction as the
// event it carries, so it is neither lost nor sent for a change that was rolled back; it is
// signed with the secret of the webhook and tried again with growing pauses.

// Webhook headers.
const (
	HeaderSignature = "X-Vynel-Signature" // sha256=<hex HMAC-SHA256 of "<timestamp>.<body>">
	HeaderTimestamp = "X-Vynel-Timestamp" // unix seconds; refuse old ones to stop replays
	HeaderEvent     = "X-Vynel-Event"
	HeaderDelivery  = "X-Vynel-Delivery"
)

// OffReasonFailures is why the panel switched a webhook off: deliveries kept failing.
const OffReasonFailures = "failures"

const (
	webhookTimeout   = 10 * time.Second
	webhookMaxTries  = 8
	webhookOffAfter  = 10 // deliveries that gave up in a row turn a webhook off
	webhookKeepDays  = 30
	webhookBodyLimit = 512
)

// webhookBackoff is the pause after the n-th failed attempt.
var webhookBackoff = []time.Duration{10 * time.Second, time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour, 12 * time.Hour}

// SignWebhook is the signature of a delivery body.
func SignWebhook(secret string, ts int64, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(strconv.FormatInt(ts, 10)))
	m.Write([]byte("."))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func hookClient(private bool) *http.Client {
	d := &net.Dialer{Timeout: 6 * time.Second}
	if !private {
		d.Control = publicOnly
	}
	return &http.Client{
		Timeout:       webhookTimeout,
		Transport:     &http.Transport{DialContext: d.DialContext, TLSHandshakeTimeout: 6 * time.Second, ResponseHeaderTimeout: webhookTimeout, DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

var (
	hookPublic  = hookClient(false)
	hookPrivate = hookClient(true)
)

// WebhookInput creates or edits a webhook.
type WebhookInput struct {
	Name         string
	URL          string
	Events       []string // types, prefixes ("user.*") or "*"; none = everything
	Enabled      bool
	AllowPrivate bool // may deliver to local and private addresses (a bot on the same server)
}

func (in *WebhookInput) check() error {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len([]rune(in.Name)) > 64 {
		return invalid("название вебхука: от 1 до 64 символов")
	}
	in.URL = strings.TrimSpace(in.URL)
	u, err := url.Parse(in.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || len(in.URL) > 512 {
		return invalid("адрес вебхука: полный URL, http:// или https://")
	}
	if u.User != nil {
		return invalid("адрес вебхука: без логина и пароля в URL (отправителя подтверждает подпись)")
	}
	host := strings.ToLower(u.Hostname())
	ip := net.ParseIP(host)
	local := host == "localhost" || strings.HasSuffix(host, ".localhost") ||
		(ip != nil && (ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()))
	if local && !in.AllowPrivate && !FetchPrivate {
		return invalid("%s — локальный адрес: разрешите локальные адреса для этого вебхука или возьмите публичный", host)
	}
	known := map[string]bool{}
	for _, t := range store.APIEventTypes() {
		known[t] = true
	}
	evs := []string{}
	for _, e := range in.Events {
		if e = strings.TrimSpace(e); e == "" {
			continue
		}
		prefix, wild := strings.CutSuffix(e, "*")
		ok := known[e]
		if wild {
			ok = e == "*"
			for t := range known {
				ok = ok || strings.HasPrefix(t, prefix)
			}
		}
		if !ok {
			return invalid("неизвестное событие «%s»", e)
		}
		evs = append(evs, e)
	}
	if len(evs) == 0 {
		evs = []string{"*"}
	}
	in.Events = evs
	return nil
}

func newWebhookSecret() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "whsec_" + base64.RawURLEncoding.EncodeToString(b), nil
}

// Webhooks lists the webhooks.
func (s *Service) Webhooks(ctx context.Context) ([]*store.Webhook, error) {
	return store.ListWebhooks(ctx, s.st.DB)
}

// Webhook loads one webhook.
func (s *Service) Webhook(ctx context.Context, id int64) (*store.Webhook, error) {
	return store.GetWebhook(ctx, s.st.DB, id)
}

// CreateWebhook adds a webhook with a new secret.
func (s *Service) CreateWebhook(ctx context.Context, actor Actor, in WebhookInput) (*store.Webhook, error) {
	if err := in.check(); err != nil {
		return nil, err
	}
	secret, err := newWebhookSecret()
	if err != nil {
		return nil, err
	}
	w := &store.Webhook{Name: in.Name, URL: in.URL, Secret: secret, Events: in.Events, Enabled: in.Enabled, AllowPrivate: in.AllowPrivate}
	err = s.mutate(ctx, change{actor: actor, action: "webhook.create", entity: "webhook", entityID: idOf(&w.ID), diff: map[string]string{"name": w.Name, "url": w.URL}},
		func(q store.DBTX) error { return store.CreateWebhook(ctx, q, w) })
	return w, err
}

// UpdateWebhook changes a webhook; switching it on clears the failures that turned it off.
func (s *Service) UpdateWebhook(ctx context.Context, actor Actor, id int64, in WebhookInput) (*store.Webhook, error) {
	if err := in.check(); err != nil {
		return nil, err
	}
	var w *store.Webhook
	err := s.mutate(ctx, change{actor: actor, action: "webhook.update", entity: "webhook", entityID: func() int64 { return id }, diff: map[string]string{"name": in.Name, "url": in.URL}},
		func(q store.DBTX) error {
			var err error
			if w, err = store.GetWebhook(ctx, q, id); err != nil {
				return err
			}
			if in.Enabled && !w.Enabled {
				w.Fails, w.OffReason = 0, ""
			}
			w.Name, w.URL, w.Events, w.Enabled, w.AllowPrivate = in.Name, in.URL, in.Events, in.Enabled, in.AllowPrivate
			return store.UpdateWebhook(ctx, q, w)
		})
	return w, err
}

// RotateWebhookSecret gives a webhook a new secret; the old one stops signing at once.
func (s *Service) RotateWebhookSecret(ctx context.Context, actor Actor, id int64) (*store.Webhook, error) {
	secret, err := newWebhookSecret()
	if err != nil {
		return nil, err
	}
	var w *store.Webhook
	err = s.mutate(ctx, change{actor: actor, action: "webhook.rotate", entity: "webhook", entityID: func() int64 { return id }},
		func(q store.DBTX) error {
			var err error
			if w, err = store.GetWebhook(ctx, q, id); err != nil {
				return err
			}
			w.Secret = secret
			return store.UpdateWebhook(ctx, q, w)
		})
	return w, err
}

// DeleteWebhook removes a webhook and what waits to be sent to it.
func (s *Service) DeleteWebhook(ctx context.Context, actor Actor, id int64) error {
	return s.mutate(ctx, change{actor: actor, action: "webhook.delete", entity: "webhook", entityID: func() int64 { return id }},
		func(q store.DBTX) error { return store.DeleteWebhook(ctx, q, id) })
}

// WebhookDeliveries lists the latest deliveries of a webhook.
func (s *Service) WebhookDeliveries(ctx context.Context, id int64, limit int) ([]*store.Delivery, error) {
	return store.ListDeliveries(ctx, s.st.DB, id, limit)
}

// TestWebhook sends a ping now and says how it went.
func (s *Service) TestWebhook(ctx context.Context, actor Actor, id int64) (*store.Delivery, error) {
	w, err := store.GetWebhook(ctx, s.st.DB, id)
	if err != nil {
		return nil, err
	}
	ev := &store.APIEvent{Type: "ping", Entity: "webhook", EntityID: id, Actor: actor.Kind + ":" + actor.ID, TS: s.now().Unix(), Data: `{"note":"test delivery"}`}
	d := &store.Delivery{WebhookID: id, Type: ev.Type, Body: string(store.EventDoc(ev))}
	if err := store.AddDelivery(ctx, s.st.DB, d); err != nil {
		return nil, err
	}
	s.deliver(ctx, w, d, false)
	return store.GetDelivery(ctx, s.st.DB, d.ID)
}

// RetryDelivery sends a delivery again from the start.
func (s *Service) RetryDelivery(ctx context.Context, webhookID, deliveryID int64) (*store.Delivery, error) {
	w, err := store.GetWebhook(ctx, s.st.DB, webhookID)
	if err != nil {
		return nil, err
	}
	d, err := store.GetDelivery(ctx, s.st.DB, deliveryID)
	if err != nil {
		return nil, err
	}
	if d.WebhookID != webhookID {
		return nil, store.ErrNotFound
	}
	d.Attempts = 0
	s.deliver(ctx, w, d, false)
	return store.GetDelivery(ctx, s.st.DB, d.ID)
}

// deliver makes one attempt and records it. final: a failure ends the delivery (no retry).
func (s *Service) deliver(ctx context.Context, w *store.Webhook, d *store.Delivery, retry bool) {
	start := time.Now()
	status, errText := s.post(ctx, w, d)
	d.Attempts++
	d.Status, d.Error, d.TookMS = status, errText, time.Since(start).Milliseconds()
	ok := errText == ""
	switch {
	case ok:
		d.State = "ok"
	case !retry || d.Attempts >= webhookMaxTries:
		d.State = "failed"
	default:
		d.State = "pending"
		d.NextAt = s.now().Add(webhookBackoff[min(d.Attempts-1, len(webhookBackoff)-1)]).Unix()
	}
	if d.State == "ok" || d.State == "failed" {
		d.NextAt = s.now().Unix()
	}
	gaveUp := d.State == "failed" && d.Type != "ping"
	_ = store.SaveDelivery(ctx, s.st.DB, d)
	_ = store.WebhookResult(ctx, s.st.DB, w.ID, status, ok, gaveUp)
	if gaveUp && w.Enabled && w.Fails+1 >= webhookOffAfter {
		s.switchOffWebhook(ctx, w.ID)
	}
}

// switchOffWebhook stops a webhook that nobody answers; an admin turns it on again.
func (s *Service) switchOffWebhook(ctx context.Context, id int64) {
	_ = s.mutate(ctx, change{actor: ActorSystem, action: "webhook.off", entity: "webhook", entityID: func() int64 { return id }}, func(q store.DBTX) error {
		w, err := store.GetWebhook(ctx, q, id)
		if err != nil {
			return err
		}
		w.Enabled, w.OffReason = false, OffReasonFailures
		return store.UpdateWebhook(ctx, q, w)
	})
}

// post sends the body and returns the HTTP status and an error text ("" = a 2xx answer).
func (s *Service) post(ctx context.Context, w *store.Webhook, d *store.Delivery) (int, string) {
	ctx, cancel := context.WithTimeout(ctx, webhookTimeout)
	defer cancel()
	body := []byte(d.Body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL, bytes.NewReader(body))
	if err != nil {
		return 0, err.Error()
	}
	ts := s.now().Unix()
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Vynel-Webhook/1")
	req.Header.Set(HeaderEvent, d.Type)
	req.Header.Set(HeaderDelivery, strconv.FormatInt(d.ID, 10))
	req.Header.Set(HeaderTimestamp, strconv.FormatInt(ts, 10))
	req.Header.Set(HeaderSignature, SignWebhook(w.Secret, ts, body))
	hc := hookPublic
	if w.AllowPrivate {
		hc = hookPrivate
	}
	resp, err := hc.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return 0, err.Error()
	}
	defer resp.Body.Close()
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, webhookBodyLimit))
	if resp.StatusCode/100 == 2 {
		return resp.StatusCode, ""
	}
	msg := strings.TrimSpace(string(snippet))
	if msg == "" {
		msg = http.StatusText(resp.StatusCode)
	}
	return resp.StatusCode, fmt.Sprintf("HTTP %d: %s", resp.StatusCode, msg)
}

// RunWebhooks delivers what is due until ctx ends, and tells when a node comes and goes.
func (s *Service) RunWebhooks(ctx context.Context) error {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	busy := map[int64]bool{}
	done := make(chan int64, 64)
	var lastNodes time.Time
	var inflight sync.WaitGroup
	for {
		select {
		case <-ctx.Done():
			inflight.Wait() // the deliveries in progress end with the context
			return nil
		case id := <-done:
			delete(busy, id)
		case <-tick.C:
		}
		if time.Since(lastNodes) >= 10*time.Second {
			lastNodes = time.Now()
			s.pollNodes(ctx)
		}
		due, err := store.DueDeliveries(ctx, s.st.DB, 200)
		if err != nil || len(due) == 0 {
			continue
		}
		group := map[int64][]*store.Delivery{}
		for _, d := range due {
			if !busy[d.WebhookID] {
				group[d.WebhookID] = append(group[d.WebhookID], d)
			}
		}
		for id, ds := range group {
			w, err := store.GetWebhook(ctx, s.st.DB, id)
			if err != nil {
				continue
			}
			_ = w
			busy[id] = true
			inflight.Add(1)
			go func() {
				defer inflight.Done()
				for _, d := range ds {
					if ctx.Err() != nil {
						break
					}
					w, err := store.GetWebhook(ctx, s.st.DB, id) // switched off or removed since
					if err != nil {
						break
					}
					if !w.Enabled {
						d.State, d.Error = "failed", "webhook is off"
						_ = store.SaveDelivery(ctx, s.st.DB, d)
						continue
					}
					s.deliver(ctx, w, d, true)
				}
				select {
				case done <- id:
				case <-ctx.Done():
				}
			}()
		}
	}
}

// pollNodes publishes node.online and node.offline when a node's live session appears or goes,
// once the state has held for two polls (a reconnect in a second is not news).
func (s *Service) pollNodes(ctx context.Context) {
	if s.NodeConnected == nil {
		return
	}
	nodes, err := store.ListNodes(ctx, s.st.DB)
	if err != nil {
		return
	}
	type change struct {
		node *store.Node
		up   bool
	}
	var changes []change
	a := &s.api
	a.mu.Lock()
	if a.nodeSeen == nil {
		a.nodeSeen, a.nodeSent, a.nodesUp = map[int64]bool{}, map[int64]bool{}, map[int64]int{}
	}
	for _, n := range nodes {
		now := s.NodeConnected(n.ID)
		sent, known := a.nodeSent[n.ID]
		if !known {
			a.nodeSent[n.ID], a.nodeSeen[n.ID] = now, now // the first look is not news
			continue
		}
		if now != a.nodeSeen[n.ID] {
			a.nodeSeen[n.ID], a.nodesUp[n.ID] = now, 0
		}
		if a.nodeSeen[n.ID] == sent {
			continue
		}
		if a.nodesUp[n.ID]++; a.nodesUp[n.ID] < 2 {
			continue
		}
		a.nodeSent[n.ID] = now
		changes = append(changes, change{n, now})
	}
	a.mu.Unlock()
	for _, c := range changes {
		typ := "node.offline"
		if c.up {
			typ = "node.online"
		}
		_ = s.st.Tx(ctx, func(q store.DBTX) error {
			return store.PublishAPIEvent(ctx, q, &store.APIEvent{Type: typ, Entity: "node", EntityID: c.node.ID, Actor: "system",
				Data: `{"code":` + jsonString(c.node.Code) + `,"name":` + jsonString(c.node.Name) + `}`})
		})
	}
}

// pruneAPI forgets old events, finished deliveries and idempotency records.
func (s *Service) pruneAPI(ctx context.Context) error {
	return store.PruneAPI(ctx, s.st.DB, webhookKeepDays)
}
