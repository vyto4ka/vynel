package webapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
	"github.com/vyto4ka/vynel/internal/panel/subscription"
)

func (s *Server) subscriptionRoutes() {
	s.handle("GET /api/subscription", s.subConfig)
	s.handle("PUT /api/subscription", s.subSave)
	s.handle("POST /api/subscription/reset", s.subReset)
	s.handle("POST /api/subscription/test", s.subTest)
	s.api.HandleFunc("GET /api/subscription/preview", s.subPreview)
}

func (s *Server) subConfig(r *http.Request) (any, error) {
	ctx := r.Context()
	hs, err := s.svc.SubHeaders(ctx)
	if err != nil {
		return nil, err
	}
	page, err := s.svc.SubPage(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"basics": s.svc.SubBasicsGet(ctx), "headers": hs, "page": page,
		"headerCatalog": service.SubHeaderCatalog, "appCatalog": service.SubAppCatalog,
		"defaultHeaders": service.DefaultSubHeaders(), "defaultPage": service.DefaultSubPage(),
		"variables": service.SubVariables, "uaRules": subscription.UARules(),
	}, nil
}

type subDraft struct {
	Basics  *service.SubBasics   `json:"basics"`
	Headers *[]service.SubHeader `json:"headers"`
	Page    *service.SubPage     `json:"page"`
}

func (s *Server) subSave(r *http.Request) (any, error) {
	var in subDraft
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	ctx, a := r.Context(), actor(r)
	if in.Basics != nil {
		if err := s.svc.SetSubBasics(ctx, a, *in.Basics); err != nil {
			return nil, err
		}
	}
	if in.Headers != nil {
		hs := *in.Headers
		if hs == nil {
			hs = []service.SubHeader{}
		}
		if err := s.svc.SetSubHeaders(ctx, a, hs); err != nil {
			return nil, err
		}
	}
	if in.Page != nil {
		if err := s.svc.SetSubPage(ctx, a, in.Page); err != nil {
			return nil, err
		}
	}
	return s.subConfig(r)
}

func (s *Server) subReset(r *http.Request) (any, error) {
	var in struct{ Part string }
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	ctx, a := r.Context(), actor(r)
	switch in.Part {
	case "headers":
		if err := s.svc.SetSubHeaders(ctx, a, nil); err != nil {
			return nil, err
		}
	case "page":
		if err := s.svc.SetSubPage(ctx, a, nil); err != nil {
			return nil, err
		}
	default:
		return nil, badRequest("part must be headers or page")
	}
	return s.subConfig(r)
}

// subUser is the user a preview is made for: the given one, else the first, else a demo.
func (s *Server) subUser(r *http.Request, id int64) *store.User {
	ctx := r.Context()
	if id > 0 {
		if u, err := s.svc.User(ctx, id); err == nil {
			return u
		}
	}
	if us, err := s.svc.Users(ctx, store.UserFilter{Limit: 1}); err == nil && len(us) > 0 {
		return us[0]
	}
	exp := time.Now().AddDate(0, 1, 0).Unix()
	limit := int64(100 << 30)
	return &store.User{Username: "demo", Status: store.StatusActive, SubToken: "demo-token", ExpireAt: &exp,
		TrafficLimitBytes: &limit, TrafficUsedBytes: 23 << 30}
}

func (s *Server) subURLFor(r *http.Request, u *store.User) string {
	if url, err := s.svc.SubscriptionURL(r.Context(), u); err == nil {
		return url
	}
	return "https://example.com/s/" + u.SubToken
}

// subTest shows what a client gets: format and headers (no device is registered).
func (s *Server) subTest(r *http.Request) (any, error) {
	var in struct {
		UserID    int64                `json:"userId"`
		UserAgent string               `json:"userAgent"`
		Headers   *[]service.SubHeader `json:"headers"` // unsaved draft
		Basics    *service.SubBasics   `json:"basics"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	ctx := r.Context()
	u := s.subUser(r, in.UserID)
	hs, err := s.svc.SubHeaders(ctx)
	if err != nil {
		return nil, err
	}
	if in.Headers != nil {
		hs = *in.Headers
	}
	b := s.svc.SubBasicsGet(ctx)
	if in.Basics != nil {
		b = *in.Basics
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
	req.Header.Set("User-Agent", in.UserAgent)
	format := subscription.Detect(req, "", u.ClientType)
	headers := subscription.RenderHeaders(hs, subscription.UserVars(u, s.subURLFor(r, u), b, time.Now()), in.UserAgent)
	if headers == nil {
		headers = []subscription.HeaderValue{}
	}
	return map[string]any{"user": u.Username, "format": format, "headers": headers}, nil
}

// subPreview renders the subscription page inside the panel (an iframe). A draft page config
// comes base64url-encoded in ?draft=, so changes are seen before saving.
func (s *Server) subPreview(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(cookieName)
	if err != nil {
		http.Error(w, "not logged in", http.StatusUnauthorized)
		return
	}
	if _, ok := s.verify(r.Context(), c.Value); !ok {
		http.Error(w, "session expired", http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	page, _ := s.svc.SubPage(ctx)
	b := s.svc.SubBasicsGet(ctx)
	if raw := r.URL.Query().Get("draft"); raw != "" {
		js, err := base64.RawURLEncoding.DecodeString(raw)
		var d subDraft
		if err != nil || json.Unmarshal(js, &d) != nil {
			http.Error(w, "bad draft", http.StatusBadRequest)
			return
		}
		if d.Page != nil {
			if err := service.CheckSubPage(d.Page); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			page = *d.Page
		}
		if d.Basics != nil {
			b = *d.Basics
		}
	}
	id, _ := strconv.ParseInt(r.URL.Query().Get("user"), 10, 64)
	u := s.subUser(r, id)
	d, err := subscription.BuildPage(u, s.subURLFor(r, u), b, page, time.Now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := subscription.RenderPage(&buf, d); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// The page is shown in a frame of the panel; its inline script and styles are its own.
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("X-Frame-Options", "SAMEORIGIN")
	h.Set("Content-Security-Policy", "default-src 'none'; img-src data:; style-src 'unsafe-inline'; script-src 'unsafe-inline'; frame-ancestors 'self'")
	_, _ = w.Write(buf.Bytes())
}
