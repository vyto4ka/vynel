package webapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
)

// The «API» page: tokens, webhooks and the events for the people who run the panel. It is the
// panel's own API (cookie session); the public API that tokens open is in publicapi.go.

func (s *Server) integrationRoutes() {
	s.handle("GET /api/integrations", s.integrations)
	s.handle("POST /api/integrations/tokens", s.createToken)
	s.handle("PATCH /api/integrations/tokens/{id}", s.updateToken)
	s.handle("POST /api/integrations/tokens/{id}/revoke", s.revokeToken)
	s.handle("DELETE /api/integrations/tokens/{id}", s.deleteToken)
	s.handle("POST /api/integrations/webhooks", s.createWebhook)
	s.handle("PATCH /api/integrations/webhooks/{id}", s.updateWebhook)
	s.handle("DELETE /api/integrations/webhooks/{id}", s.deleteWebhook)
	s.handle("POST /api/integrations/webhooks/{id}/rotate", s.rotateWebhook)
	s.handle("POST /api/integrations/webhooks/{id}/test", s.testWebhook)
	s.handle("GET /api/integrations/webhooks/{id}/deliveries", s.webhookDeliveries)
	s.handle("POST /api/integrations/webhooks/{id}/deliveries/{delivery}/retry", s.retryDelivery)
	s.handle("GET /api/integrations/events", s.recentEvents)
}

type tokenDTO struct {
	ID         int64    `json:"id"`
	Name       string   `json:"name"`
	Prefix     string   `json:"prefix"`
	Scopes     []string `json:"scopes"`
	CIDRs      []string `json:"cidrs"`
	RateLimit  int      `json:"rateLimit"`
	ExpiresAt  *int64   `json:"expiresAt"`
	CreatedBy  string   `json:"createdBy"`
	CreatedAt  int64    `json:"createdAt"`
	LastUsedAt *int64   `json:"lastUsedAt"`
	LastUsedIP string   `json:"lastUsedIp"`
	Uses       int64    `json:"uses"`
	RevokedAt  *int64   `json:"revokedAt"`
	State      string   `json:"state"` // active|expired|revoked
}

func (s *Server) tokenDTO(t *store.APIToken) tokenDTO {
	state := "active"
	switch {
	case t.RevokedAt != nil:
		state = "revoked"
	case t.ExpiresAt != nil && time.Now().Unix() >= *t.ExpiresAt:
		state = "expired"
	}
	return tokenDTO{ID: t.ID, Name: t.Name, Prefix: t.Prefix, Scopes: t.Scopes, CIDRs: t.CIDRs, RateLimit: t.RateLimit, ExpiresAt: t.ExpiresAt,
		CreatedBy: t.CreatedBy, CreatedAt: t.CreatedAt, LastUsedAt: t.LastUsedAt, LastUsedIP: t.LastUsedIP, Uses: t.Uses, RevokedAt: t.RevokedAt, State: state}
}

type webhookDTO struct {
	ID           int64    `json:"id"`
	Name         string   `json:"name"`
	URL          string   `json:"url"`
	Events       []string `json:"events"`
	Enabled      bool     `json:"enabled"`
	AllowPrivate bool     `json:"allowPrivate"`
	Fails        int      `json:"fails"`
	OffReason    string   `json:"offReason"`
	CreatedAt    int64    `json:"createdAt"`
	LastAt       *int64   `json:"lastAt"`
	LastStatus   int      `json:"lastStatus"`
	SecretHint   string   `json:"secretHint"`
	Secret       string   `json:"secret,omitempty"` // only when it has just been made or changed
}

func webhookView(w *store.Webhook, reveal bool) webhookDTO {
	d := webhookDTO{ID: w.ID, Name: w.Name, URL: w.URL, Events: w.Events, Enabled: w.Enabled, AllowPrivate: w.AllowPrivate, Fails: w.Fails,
		OffReason: w.OffReason, CreatedAt: w.CreatedAt, LastAt: w.LastAt, LastStatus: w.LastStatus}
	if n := len(w.Secret); n > 4 {
		d.SecretHint = "…" + w.Secret[n-4:]
	}
	if reveal {
		d.Secret = w.Secret
	}
	return d
}

type deliveryDTO struct {
	ID        int64  `json:"id"`
	EventID   int64  `json:"eventId"`
	Type      string `json:"type"`
	State     string `json:"state"`
	Attempts  int    `json:"attempts"`
	NextAt    int64  `json:"nextAt"`
	CreatedAt int64  `json:"createdAt"`
	Status    int    `json:"status"`
	Error     string `json:"error"`
	TookMS    int64  `json:"tookMs"`
	Body      string `json:"body"`
}

func deliveryView(d *store.Delivery) deliveryDTO {
	return deliveryDTO{ID: d.ID, EventID: d.EventID, Type: d.Type, State: d.State, Attempts: d.Attempts, NextAt: d.NextAt, CreatedAt: d.CreatedAt,
		Status: d.Status, Error: d.Error, TookMS: d.TookMS, Body: d.Body}
}

func (s *Server) integrations(r *http.Request) (any, error) {
	ctx := r.Context()
	ts, err := s.svc.APITokens(ctx)
	if err != nil {
		return nil, err
	}
	tokens := make([]tokenDTO, 0, len(ts))
	for _, t := range ts {
		tokens = append(tokens, s.tokenDTO(t))
	}
	ws, err := s.svc.Webhooks(ctx)
	if err != nil {
		return nil, err
	}
	hooks := make([]webhookDTO, 0, len(ws))
	for _, w := range ws {
		hooks = append(hooks, webhookView(w, false))
	}
	base := s.apiBase(r)
	return map[string]any{
		"baseUrl": base, "specUrl": base + "/openapi.json", "scopes": service.APIScopes, "events": store.APIEventTypes(),
		"defaultRate": service.DefaultAPIRate, "tokens": tokens, "webhooks": hooks,
		"headers": map[string]string{"signature": service.HeaderSignature, "timestamp": service.HeaderTimestamp, "event": service.HeaderEvent, "delivery": service.HeaderDelivery},
	}, nil
}

func (s *Server) createToken(r *http.Request) (any, error) {
	var in struct {
		Name      string   `json:"name"`
		Scopes    []string `json:"scopes"`
		CIDRs     []string `json:"cidrs"`
		RateLimit int      `json:"rateLimit"`
		ExpiresAt *int64   `json:"expiresAt"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	ti := service.APITokenInput{Name: in.Name, Scopes: in.Scopes, CIDRs: in.CIDRs, RateLimit: in.RateLimit}
	if in.ExpiresAt != nil {
		t := time.Unix(*in.ExpiresAt, 0)
		ti.ExpiresAt = &t
	}
	t, secret, err := s.svc.CreateAPIToken(r.Context(), actor(r), ti)
	if err != nil {
		return nil, err
	}
	return map[string]any{"token": s.tokenDTO(t), "secret": secret}, nil
}

func (s *Server) updateToken(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	var in struct {
		Name      string   `json:"name"`
		Scopes    []string `json:"scopes"`
		CIDRs     []string `json:"cidrs"`
		RateLimit int      `json:"rateLimit"`
		ExpiresAt *int64   `json:"expiresAt"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	ti := service.APITokenInput{Name: in.Name, Scopes: in.Scopes, CIDRs: in.CIDRs, RateLimit: in.RateLimit}
	if in.ExpiresAt != nil {
		t := time.Unix(*in.ExpiresAt, 0)
		ti.ExpiresAt = &t
	}
	t, err := s.svc.UpdateAPIToken(r.Context(), actor(r), id, ti)
	if err != nil {
		return nil, err
	}
	return s.tokenDTO(t), nil
}

func (s *Server) revokeToken(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	return nil, s.svc.RevokeAPIToken(r.Context(), actor(r), id)
}

func (s *Server) deleteToken(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	return nil, s.svc.DeleteAPIToken(r.Context(), actor(r), id)
}

func webhookInput(r *http.Request) (service.WebhookInput, error) {
	var in struct {
		Name         string   `json:"name"`
		URL          string   `json:"url"`
		Events       []string `json:"events"`
		Enabled      bool     `json:"enabled"`
		AllowPrivate bool     `json:"allowPrivate"`
	}
	if err := decode(r, &in); err != nil {
		return service.WebhookInput{}, err
	}
	return service.WebhookInput{Name: in.Name, URL: in.URL, Events: in.Events, Enabled: in.Enabled, AllowPrivate: in.AllowPrivate}, nil
}

func (s *Server) createWebhook(r *http.Request) (any, error) {
	in, err := webhookInput(r)
	if err != nil {
		return nil, err
	}
	w, err := s.svc.CreateWebhook(r.Context(), actor(r), in)
	if err != nil {
		return nil, err
	}
	return webhookView(w, true), nil
}

func (s *Server) updateWebhook(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	in, err := webhookInput(r)
	if err != nil {
		return nil, err
	}
	w, err := s.svc.UpdateWebhook(r.Context(), actor(r), id, in)
	if err != nil {
		return nil, err
	}
	return webhookView(w, false), nil
}

func (s *Server) deleteWebhook(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	return nil, s.svc.DeleteWebhook(r.Context(), actor(r), id)
}

func (s *Server) rotateWebhook(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	w, err := s.svc.RotateWebhookSecret(r.Context(), actor(r), id)
	if err != nil {
		return nil, err
	}
	return webhookView(w, true), nil
}

func (s *Server) testWebhook(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	d, err := s.svc.TestWebhook(r.Context(), actor(r), id)
	if err != nil {
		return nil, err
	}
	return deliveryView(d), nil
}

func (s *Server) webhookDeliveries(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	ds, err := s.svc.WebhookDeliveries(r.Context(), id, limit)
	if err != nil {
		return nil, err
	}
	out := make([]deliveryDTO, 0, len(ds))
	for _, d := range ds {
		out = append(out, deliveryView(d))
	}
	return out, nil
}

func (s *Server) retryDelivery(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	did, err := pathID(r, "delivery")
	if err != nil {
		return nil, err
	}
	d, err := s.svc.RetryDelivery(r.Context(), id, did)
	if err != nil {
		return nil, err
	}
	return deliveryView(d), nil
}

// recentEvents is the latest events, newest first, to see what a webhook would have received.
func (s *Server) recentEvents(r *http.Request) (any, error) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	db := s.svc.Store().DB
	last, err := store.LastAPIEventID(r.Context(), db)
	if err != nil {
		return nil, err
	}
	evs, err := store.ListAPIEvents(r.Context(), db, max(0, last-int64(limit)), nil, limit)
	if err != nil {
		return nil, err
	}
	out := make([]json.RawMessage, 0, len(evs))
	for i := len(evs) - 1; i >= 0; i-- {
		out = append(out, store.EventDoc(evs[i]))
	}
	return out, nil
}
