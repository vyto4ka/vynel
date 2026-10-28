package webapi

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
	"github.com/vyto4ka/vynel/internal/panel/subscription"
	"github.com/vyto4ka/vynel/internal/xrayconf"
)

// v1Routes registers the public API. The list is the contract: openapi.yaml describes the same
// endpoints (a test compares them), and docs/API.md the same behavior.
func (s *Server) v1Routes() {
	s.v1Open("GET", "/openapi.yaml", s.v1Spec(false))
	s.v1Open("GET", "/openapi.json", s.v1Spec(true))
	s.v1("GET", "/me", "", s.v1Me)

	s.v1("GET", "/users", service.ScopeUsersRead, s.v1Users)
	s.v1("POST", "/users", service.ScopeUsersWrite, s.v1CreateUser)
	s.v1("GET", "/users/{id}", service.ScopeUsersRead, s.v1User)
	s.v1("PATCH", "/users/{id}", service.ScopeUsersWrite, s.v1PatchUser)
	s.v1("DELETE", "/users/{id}", service.ScopeUsersDelete, s.v1DeleteUser)
	s.v1("POST", "/users/{id}/extend", service.ScopeUsersWrite, s.v1Extend)
	s.v1("POST", "/users/{id}/reset-traffic", service.ScopeUsersWrite, s.v1ResetTraffic)
	s.v1("POST", "/users/{id}/enable", service.ScopeUsersWrite, s.v1SetEnabled(true))
	s.v1("POST", "/users/{id}/disable", service.ScopeUsersWrite, s.v1SetEnabled(false))
	s.v1("POST", "/users/{id}/reissue", service.ScopeUsersWrite, s.v1Reissue)
	s.v1("GET", "/users/{id}/subscription", service.ScopeUsersRead, s.v1Subscription)
	s.v1("GET", "/users/{id}/traffic", service.ScopeUsersRead, s.v1UserTraffic)
	s.v1("GET", "/users/{id}/devices", service.ScopeUsersRead, s.v1Devices)
	s.v1("DELETE", "/users/{id}/devices/{device_id}", service.ScopeUsersWrite, s.v1DeleteDevice)

	s.v1("GET", "/groups", service.ScopeGroupsRead, s.v1Groups)
	s.v1("GET", "/groups/{id}", service.ScopeGroupsRead, s.v1Group)
	s.v1("GET", "/user-templates", service.ScopeTemplatesRead, s.v1Templates)

	s.v1("GET", "/nodes", service.ScopeNodesRead, s.v1Nodes)
	s.v1("GET", "/nodes/{id}", service.ScopeNodesRead, s.v1Node)
	s.v1("GET", "/stats/overview", service.ScopeStatsRead, s.v1Overview)
	s.v1("GET", "/events", service.ScopeEventsRead, s.v1Events)

	s.api.HandleFunc("/api/v1/", s.v1Catchall)
}

// ---- me ----

func (s *Server) v1Me(r *http.Request, c *v1Ctx) (any, error) {
	t := c.tok
	cidrs := t.CIDRs
	if cidrs == nil {
		cidrs = []string{}
	}
	rate := t.RateLimit
	if rate <= 0 {
		rate = service.DefaultAPIRate
	}
	return map[string]any{
		"token": map[string]any{"id": t.ID, "name": t.Name, "prefix": t.Prefix, "scopes": t.Scopes, "allowed_networks": cidrs,
			"rate_limit_per_minute": rate, "expires_at": rfc3339(t.ExpiresAt), "created_at": rfc3339v(t.CreatedAt)},
		"server": map[string]any{"version": s.cfg.Version, "time": time.Now().UTC().Format(time.RFC3339)},
	}, nil
}

// ---- users ----

type v1User struct {
	ID                int64   `json:"id"`
	Username          string  `json:"username"`
	Status            string  `json:"status"`
	Enabled           bool    `json:"enabled"`
	ExpireAt          *string `json:"expire_at"`
	TrafficLimitBytes *int64  `json:"traffic_limit_bytes"`
	TrafficUsedBytes  int64   `json:"traffic_used_bytes"`
	LifetimeBytes     int64   `json:"lifetime_traffic_bytes"`
	ResetStrategy     string  `json:"reset_strategy"`
	HWIDLimit         *int64  `json:"hwid_limit"`
	HWIDOff           bool    `json:"hwid_off"`
	IPLimit           *int64  `json:"ip_limit"`
	ClientType        string  `json:"client_type"`
	Note              string  `json:"note"`
	TelegramID        *int64  `json:"telegram_id"`
	ExternalID        *string `json:"external_id"`
	GroupIDs          []int64 `json:"group_ids"`
	TemplateID        *int64  `json:"template_id"`
	UUID              string  `json:"uuid"`
	OnlineAt          *string `json:"online_at"`
	CreatedAt         string  `json:"created_at"`
	SubscriptionURL   any     `json:"subscription_url,omitempty"` // a link, or null while the subscription domain is not set; absent in lists
}

func (s *Server) v1UserDTO(r *http.Request, u *store.User, groups []int64, withURL bool) v1User {
	if groups == nil {
		groups = []int64{}
	}
	d := v1User{ID: u.ID, Username: u.Username, Status: u.Status, Enabled: !u.Disabled, ExpireAt: rfc3339(u.ExpireAt),
		TrafficLimitBytes: u.TrafficLimitBytes, TrafficUsedBytes: u.TrafficUsedBytes, LifetimeBytes: u.LifetimeUsedBytes,
		ResetStrategy: u.ResetStrategy, HWIDLimit: u.HWIDLimit, HWIDOff: u.HWIDOff, IPLimit: u.IPLimit, ClientType: u.ClientType, Note: u.Note,
		TelegramID: u.TelegramID, ExternalID: u.ExternalID, GroupIDs: groups, TemplateID: u.TemplateID, UUID: u.UUID,
		OnlineAt: rfc3339(u.OnlineAt), CreatedAt: rfc3339v(u.CreatedAt)}
	if withURL {
		d.SubscriptionURL = (*string)(nil)
		if url, err := s.svc.SubscriptionURL(r.Context(), u); err == nil {
			d.SubscriptionURL = &url
		}
	}
	return d
}

var userSortKeys = []string{"username", "status", "traffic", "expire", "groups", "online", "created"}

func (s *Server) v1Users(r *http.Request, c *v1Ctx) (any, error) {
	ctx, q := r.Context(), r.URL.Query()
	limit, offset, err := v1Page(r, 50, 500)
	if err != nil {
		return nil, err
	}
	withURL := slices.Contains(strings.Split(q.Get("include"), ","), "subscription")
	f := store.UserFilter{Status: q.Get("status"), Search: q.Get("search"), Sort: q.Get("sort"), Desc: q.Get("order") == "desc", Limit: limit, Offset: offset}
	if f.Status != "" && !slices.Contains([]string{store.StatusActive, store.StatusDisabled, store.StatusLimited, store.StatusExpired}, f.Status) {
		return nil, apiErr(http.StatusBadRequest, "bad_parameter", "status is active, disabled, limited or expired").on("status")
	}
	if f.Sort != "" && !slices.Contains(userSortKeys, f.Sort) {
		return nil, apiErr(http.StatusBadRequest, "bad_parameter", "sort is one of "+strings.Join(userSortKeys, ", ")).on("sort")
	}
	if v := q.Get("telegram_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return nil, apiErr(http.StatusBadRequest, "bad_parameter", "telegram_id is a number").on("telegram_id")
		}
		f.TelegramID = &id
	}
	if v := q.Get("external_id"); v != "" {
		f.ExternalID = &v
	}
	var us []*store.User
	total := 0
	if name := q.Get("username"); name != "" { // an exact name: at most one
		if u, err := s.svc.UserByUsername(ctx, name); err == nil {
			us, total = []*store.User{u}, 1
		}
	} else {
		if us, err = s.svc.Users(ctx, f); err != nil {
			return nil, err
		}
		if total, err = s.svc.CountUsers(ctx, f); err != nil {
			return nil, err
		}
	}
	groups, err := s.svc.UsersGroups(ctx, us)
	if err != nil {
		return nil, err
	}
	items := make([]v1User, 0, len(us))
	for _, u := range us {
		items = append(items, s.v1UserDTO(r, u, groups[u.ID], withURL))
	}
	return map[string]any{"items": items, "total": total, "limit": limit, "offset": offset}, nil
}

func (s *Server) v1LoadUser(r *http.Request) (*store.User, error) {
	id, err := v1ID(r, "id")
	if err != nil {
		return nil, err
	}
	u, err := s.svc.User(r.Context(), id)
	if err != nil {
		return nil, notFoundOr(err, "user")
	}
	return u, nil
}

// notFoundOr names what was not found; other errors pass.
func notFoundOr(err error, what string) error {
	if isNotFound(err) {
		return notFoundErr(what)
	}
	return err
}

func (s *Server) v1Reply(r *http.Request, id int64, u *store.User) (any, error) {
	if u == nil {
		var err error
		if u, err = s.svc.User(r.Context(), id); err != nil {
			return nil, notFoundOr(err, "user")
		}
	}
	g, err := s.svc.UserGroupIDs(r.Context(), u.ID)
	if err != nil {
		return nil, err
	}
	return s.v1UserDTO(r, u, g, true), nil
}

func (s *Server) v1User(r *http.Request, c *v1Ctx) (any, error) {
	u, err := s.v1LoadUser(r)
	if err != nil {
		return nil, err
	}
	return s.v1Reply(r, u.ID, u)
}

// v1Time reads an RFC3339 time from a request.
func v1Time(field, v string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return t, invalidField(field, field+" is a time like 2026-12-31T23:59:59Z")
	}
	return t, nil
}

func (s *Server) v1CreateUser(r *http.Request, c *v1Ctx) (any, error) {
	var in struct {
		Username          string  `json:"username"`
		TemplateID        int64   `json:"template_id"`
		GroupIDs          []int64 `json:"group_ids"`
		Note              string  `json:"note"`
		ExpireAt          *string `json:"expire_at"`
		NeverExpires      bool    `json:"never_expires"`
		TrafficLimitBytes *int64  `json:"traffic_limit_bytes"`
		ResetStrategy     string  `json:"reset_strategy"`
		HWIDLimit         *int64  `json:"hwid_limit"`
		IPLimit           *int64  `json:"ip_limit"`
		ClientType        string  `json:"client_type"`
		TelegramID        *int64  `json:"telegram_id"`
		ExternalID        *string `json:"external_id"`
		SubscriptionToken string  `json:"subscription_token"`
	}
	if err := v1Decode(r, &in); err != nil {
		return nil, err
	}
	ci := service.CreateUserInput{Username: in.Username, TemplateID: in.TemplateID, GroupIDs: in.GroupIDs, Note: in.Note, NeverExpires: in.NeverExpires,
		TrafficLimitBytes: in.TrafficLimitBytes, ResetStrategy: in.ResetStrategy, HWIDLimit: in.HWIDLimit, IPLimit: in.IPLimit, ClientType: in.ClientType,
		TelegramID: in.TelegramID, ExternalID: in.ExternalID, SubToken: in.SubscriptionToken}
	if in.ExpireAt != nil {
		t, err := v1Time("expire_at", *in.ExpireAt)
		if err != nil {
			return nil, err
		}
		ci.ExpireAt = &t
	}
	u, err := s.svc.CreateUser(r.Context(), v1Actor(c), ci)
	if err != nil {
		if errorsIsConflict(err) && !isIdentityTaken(err) {
			return nil, apiErr(http.StatusConflict, "username_taken", "a user with this username or subscription_token already exists").on("username")
		}
		return nil, err
	}
	out, err := s.v1Reply(r, u.ID, u)
	if err != nil {
		return nil, err
	}
	return created(out), nil
}

// v1Present decodes a body into its fields, keeping which of them were there: PATCH changes only
// those, and null is a value (no expiry, no limit).
func v1Present(r *http.Request, allowed ...string) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := v1Decode(r, &m); err != nil {
		return nil, err
	}
	for k := range m {
		if !slices.Contains(allowed, k) {
			return nil, apiErr(http.StatusBadRequest, "unknown_field", "unknown field "+strconv.Quote(k)+"; allowed: "+strings.Join(allowed, ", ")).on(k)
		}
	}
	return m, nil
}

func v1Field[T any](m map[string]json.RawMessage, key string) (v T, set bool, null bool, err error) {
	raw, ok := m[key]
	if !ok {
		return v, false, false, nil
	}
	if string(raw) == "null" {
		return v, true, true, nil
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, true, false, invalidField(key, key+" has the wrong type")
	}
	return v, true, false, nil
}

func (s *Server) v1PatchUser(r *http.Request, c *v1Ctx) (any, error) {
	id, err := v1ID(r, "id")
	if err != nil {
		return nil, err
	}
	m, err := v1Present(r, "note", "enabled", "expire_at", "traffic_limit_bytes", "reset_strategy", "hwid_limit", "hwid_off", "ip_limit",
		"client_type", "group_ids", "telegram_id", "external_id", "subscription_token")
	if err != nil {
		return nil, err
	}
	var (
		det      service.UserDetailsInput
		ident    service.UserIdentityInput
		enabled  *bool
		groups   []int64
		setGroup bool
		expire   *time.Time
		setExp   bool
		limit    *int64
		setLimit bool
		subTok   string
	)
	// Everything is read before anything is changed, so a typo in the last field changes nothing.
	if v, set, null, err := v1Field[string](m, "note"); err != nil || set {
		if err != nil {
			return nil, err
		}
		if null {
			v = ""
		}
		det.Note = &v
	}
	if v, set, null, err := v1Field[bool](m, "enabled"); err != nil || set {
		if err != nil || null {
			return nil, firstErr(err, invalidField("enabled", "enabled is true or false"))
		}
		enabled = &v
	}
	if v, set, null, err := v1Field[string](m, "expire_at"); err != nil || set {
		if err != nil {
			return nil, err
		}
		setExp = true
		if !null {
			t, err := v1Time("expire_at", v)
			if err != nil {
				return nil, err
			}
			expire = &t
		}
	}
	if v, set, null, err := v1Field[int64](m, "traffic_limit_bytes"); err != nil || set {
		if err != nil {
			return nil, err
		}
		setLimit = true
		if !null {
			limit = &v
		}
	}
	if v, set, null, err := v1Field[string](m, "reset_strategy"); err != nil || set {
		if err != nil || null {
			return nil, firstErr(err, invalidField("reset_strategy", "reset_strategy is no, day, week or month"))
		}
		det.ResetStrategy = &v
	}
	for key, dst := range map[string]**int64{"hwid_limit": &det.HWIDLimit, "ip_limit": &det.IPLimit} {
		if v, set, null, err := v1Field[int64](m, key); err != nil || set {
			if err != nil {
				return nil, err
			}
			if null { // back to the default
				v = -1
			}
			*dst = &v
		}
	}
	if v, set, null, err := v1Field[bool](m, "hwid_off"); err != nil || set {
		if err != nil || null {
			return nil, firstErr(err, invalidField("hwid_off", "hwid_off is true or false"))
		}
		det.HWIDOff = &v
	}
	if v, set, null, err := v1Field[string](m, "client_type"); err != nil || set {
		if err != nil || null {
			return nil, firstErr(err, invalidField("client_type", "client_type is a text"))
		}
		det.ClientType = &v
	}
	if v, set, null, err := v1Field[[]int64](m, "group_ids"); err != nil || set {
		if err != nil {
			return nil, err
		}
		if null || v == nil {
			v = []int64{}
		}
		groups, setGroup = v, true
	}
	if v, set, null, err := v1Field[int64](m, "telegram_id"); err != nil || set {
		if err != nil {
			return nil, err
		}
		ident.SetTelegramID = true
		if !null {
			ident.TelegramID = &v
		}
	}
	if v, set, null, err := v1Field[string](m, "external_id"); err != nil || set {
		if err != nil {
			return nil, err
		}
		ident.SetExternalID = true
		if !null {
			ident.ExternalID = &v
		}
	}
	if v, set, null, err := v1Field[string](m, "subscription_token"); err != nil || set {
		if err != nil || null {
			return nil, firstErr(err, invalidField("subscription_token", "subscription_token is a text"))
		}
		subTok = v
	}

	ctx, a := r.Context(), v1Actor(c)
	if _, err := s.svc.User(ctx, id); err != nil {
		return nil, notFoundOr(err, "user")
	}
	var u *store.User
	apply := func(got *store.User, err error) error {
		if err == nil && got != nil {
			u = got
		}
		return err
	}
	steps := []func() error{}
	if det.Note != nil || det.HWIDLimit != nil || det.HWIDOff != nil || det.IPLimit != nil || det.ClientType != nil || det.ResetStrategy != nil {
		steps = append(steps, func() error { return apply(s.svc.SetUserDetails(ctx, a, id, det)) })
	}
	if ident.SetTelegramID || ident.SetExternalID {
		steps = append(steps, func() error { return apply(s.svc.SetUserIdentity(ctx, a, id, ident)) })
	}
	if enabled != nil {
		steps = append(steps, func() error { return apply(s.svc.SetUserEnabled(ctx, a, id, *enabled)) })
	}
	if setGroup {
		steps = append(steps, func() error { return apply(s.svc.SetUserGroups(ctx, a, id, groups)) })
	}
	if setExp {
		steps = append(steps, func() error { return apply(s.svc.SetUserExpiry(ctx, a, id, expire)) })
	}
	if setLimit {
		steps = append(steps, func() error { return apply(s.svc.SetUserTrafficLimit(ctx, a, id, limit)) })
	}
	if subTok != "" {
		steps = append(steps, func() error { return apply(s.svc.SetSubToken(ctx, a, id, subTok)) })
	}
	for _, step := range steps {
		if err := step(); err != nil {
			if errorsIsConflict(err) && !isIdentityTaken(err) {
				return nil, apiErr(http.StatusConflict, "subscription_token_taken", "this subscription_token is already used").on("subscription_token")
			}
			return nil, err
		}
	}
	return s.v1Reply(r, id, u)
}

func firstErr(err, other error) error {
	if err != nil {
		return err
	}
	return other
}

func (s *Server) v1DeleteUser(r *http.Request, c *v1Ctx) (any, error) {
	id, err := v1ID(r, "id")
	if err != nil {
		return nil, err
	}
	if _, err := s.svc.User(r.Context(), id); err != nil {
		return nil, notFoundOr(err, "user")
	}
	if err := s.svc.DeleteUser(r.Context(), v1Actor(c), id); err != nil {
		return nil, err
	}
	return noContent(), nil
}

func (s *Server) v1Extend(r *http.Request, c *v1Ctx) (any, error) {
	id, err := v1ID(r, "id")
	if err != nil {
		return nil, err
	}
	var in struct {
		Months int `json:"months"`
		Days   int `json:"days"`
	}
	if err := v1Decode(r, &in); err != nil {
		return nil, err
	}
	if in.Months < 0 || in.Days < 0 || in.Months+in.Days == 0 || in.Months > 1200 || in.Days > 36500 {
		return nil, invalidField("months", "give months and/or days: positive numbers (up to 1200 months, 36500 days)")
	}
	u, err := s.svc.ExtendUser(r.Context(), v1Actor(c), id, in.Months, in.Days)
	if err != nil {
		return nil, notFoundOr(err, "user")
	}
	return s.v1Reply(r, id, u)
}

func (s *Server) v1ResetTraffic(r *http.Request, c *v1Ctx) (any, error) {
	id, err := v1ID(r, "id")
	if err != nil {
		return nil, err
	}
	u, err := s.svc.ResetUserTraffic(r.Context(), v1Actor(c), id)
	if err != nil {
		return nil, notFoundOr(err, "user")
	}
	return s.v1Reply(r, id, u)
}

func (s *Server) v1SetEnabled(on bool) v1Func {
	return func(r *http.Request, c *v1Ctx) (any, error) {
		id, err := v1ID(r, "id")
		if err != nil {
			return nil, err
		}
		u, err := s.svc.SetUserEnabled(r.Context(), v1Actor(c), id, on)
		if err != nil {
			return nil, notFoundOr(err, "user")
		}
		return s.v1Reply(r, id, u)
	}
}

func (s *Server) v1Reissue(r *http.Request, c *v1Ctx) (any, error) {
	id, err := v1ID(r, "id")
	if err != nil {
		return nil, err
	}
	in := struct {
		SubscriptionToken *bool `json:"subscription_token"`
		UUID              bool  `json:"uuid"`
	}{}
	if err := v1DecodeOptional(r, &in); err != nil {
		return nil, err
	}
	token := in.SubscriptionToken == nil || *in.SubscriptionToken // the link, unless said otherwise
	if !token && !in.UUID {
		return nil, invalidField("subscription_token", "nothing to reissue: set subscription_token or uuid")
	}
	u, err := s.svc.ReissueUser(r.Context(), v1Actor(c), id, token, in.UUID)
	if err != nil {
		return nil, notFoundOr(err, "user")
	}
	return s.v1Reply(r, id, u)
}

func (s *Server) v1Subscription(r *http.Request, c *v1Ctx) (any, error) {
	ctx := r.Context()
	u, err := s.v1LoadUser(r)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"url": nil, "links": []string{}}
	if url, err := s.svc.SubscriptionURL(ctx, u); err == nil {
		out["url"] = url
	}
	hosts, err := s.svc.CurrentHosts(ctx, u)
	if err != nil {
		return nil, err
	}
	links := []map[string]any{}
	for _, h := range hosts {
		if h.Hidden {
			continue
		}
		links = append(links, map[string]any{"remark": h.Remark, "node": h.NodeCode, "link": subscription.Link(h, u.UUID)})
	}
	out["links"] = links
	return out, nil
}

func (s *Server) v1UserTraffic(r *http.Request, c *v1Ctx) (any, error) {
	ctx := r.Context()
	u, err := s.v1LoadUser(r)
	if err != nil {
		return nil, err
	}
	days := 30
	if v := r.URL.Query().Get("days"); v != "" {
		if days, err = strconv.Atoi(v); err != nil || days < 1 || days > 365 {
			return nil, apiErr(http.StatusBadRequest, "bad_parameter", "days is a number from 1 to 365").on("days")
		}
	}
	byNode, err := s.svc.UserTrafficByNode(ctx, u.ID, days)
	if err != nil {
		return nil, err
	}
	daily, err := s.svc.TrafficByDay(ctx, u.ID, days)
	if err != nil {
		return nil, err
	}
	nodes := make([]map[string]any, 0, len(byNode))
	for _, t := range byNode {
		nodes = append(nodes, map[string]any{"node": t.Code, "bytes": t.Bytes})
	}
	perDay := make([]map[string]any, 0, len(daily))
	for _, d := range daily {
		perDay = append(perDay, map[string]any{"date": time.Unix(d.Day, 0).UTC().Format("2006-01-02"), "upload_bytes": d.Up, "download_bytes": d.Down})
	}
	return map[string]any{"days": days, "used_in_period_bytes": u.TrafficUsedBytes, "lifetime_bytes": u.LifetimeUsedBytes, "by_node": nodes, "by_day": perDay}, nil
}

func (s *Server) v1Devices(r *http.Request, c *v1Ctx) (any, error) {
	u, err := s.v1LoadUser(r)
	if err != nil {
		return nil, err
	}
	devs, err := s.svc.Devices(r.Context(), u.ID)
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(devs))
	for _, d := range devs {
		items = append(items, map[string]any{"id": d.ID, "hwid": d.HWID, "platform": d.Platform, "os_version": d.OSVersion, "model": d.Model,
			"user_agent": d.UserAgent, "first_seen_at": rfc3339v(d.FirstSeen), "last_seen_at": rfc3339v(d.LastSeen), "last_ip": d.LastIP})
	}
	return map[string]any{"items": items, "total": len(items)}, nil
}

func (s *Server) v1DeleteDevice(r *http.Request, c *v1Ctx) (any, error) {
	u, err := s.v1LoadUser(r)
	if err != nil {
		return nil, err
	}
	did, err := v1ID(r, "device_id")
	if err != nil {
		return nil, err
	}
	if err := s.svc.DeleteDevice(r.Context(), v1Actor(c), u.ID, did); err != nil {
		return nil, notFoundOr(err, "device")
	}
	return noContent(), nil
}

// ---- groups, templates ----

func (s *Server) v1GroupDTO(g *store.Group, members map[int64]int) map[string]any {
	return map[string]any{"id": g.ID, "name": g.Name, "description": g.Description, "member_count": members[g.ID], "created_at": rfc3339v(g.CreatedAt)}
}

func (s *Server) v1Groups(r *http.Request, c *v1Ctx) (any, error) {
	gs, err := s.svc.Groups(r.Context())
	if err != nil {
		return nil, err
	}
	members, err := s.svc.GroupMemberCounts(r.Context())
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(gs))
	for _, g := range gs {
		items = append(items, s.v1GroupDTO(g, members))
	}
	return map[string]any{"items": items, "total": len(items)}, nil
}

func (s *Server) v1Group(r *http.Request, c *v1Ctx) (any, error) {
	id, err := v1ID(r, "id")
	if err != nil {
		return nil, err
	}
	gs, err := s.svc.Groups(r.Context())
	if err != nil {
		return nil, err
	}
	members, err := s.svc.GroupMemberCounts(r.Context())
	if err != nil {
		return nil, err
	}
	for _, g := range gs {
		if g.ID == id {
			return s.v1GroupDTO(g, members), nil
		}
	}
	return nil, notFoundErr("group")
}

func (s *Server) v1Templates(r *http.Request, c *v1Ctx) (any, error) {
	ts, err := s.svc.UserTemplates(r.Context())
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(ts))
	for _, t := range ts {
		gids := t.GroupIDs
		if gids == nil {
			gids = []int64{}
		}
		items = append(items, map[string]any{"id": t.ID, "name": t.Name, "is_default": t.IsDefault, "expire_months": t.ExpireMonths, "expire_days": t.ExpireDays,
			"traffic_limit_bytes": t.TrafficLimitBytes, "reset_strategy": t.ResetStrategy, "hwid_limit": t.HWIDLimit, "client_type": t.ClientType,
			"group_ids": gids, "note": t.Note})
	}
	return map[string]any{"items": items, "total": len(items)}, nil
}

// ---- nodes, stats ----

func (s *Server) v1NodeList(r *http.Request) ([]map[string]any, error) {
	sts, err := s.svc.NodeStatuses(r.Context(), s.cfg.Connected)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(sts))
	for _, ns := range sts {
		n := ns.Node
		problems := []string{}
		if n.LastError != "" {
			problems = append(problems, n.LastError)
		}
		problems = append(problems, n.Warnings...)
		d := map[string]any{"id": n.ID, "code": n.Code, "name": n.Name, "country": n.Country, "flag": xrayconf.CountryFlag(n.Country), "domain": n.Domain,
			"enabled": n.Enabled, "state": ns.State(), "online": ns.Connected, "last_seen_at": rfc3339(n.LastSeenAt), "xray_version": n.XrayVersion,
			"agent_version": n.AgentVersion, "today_bytes": ns.TodayBytes, "problems": problems, "metrics": nil}
		if m := ns.Metrics; m != nil {
			d["metrics"] = map[string]any{"at": rfc3339v(m.TS), "cpu_percent": m.CPU, "memory_used_bytes": m.MemUsed, "memory_total_bytes": m.MemTotal,
				"load1": m.Load1, "rx_bytes_per_sec": m.RxBps, "tx_bytes_per_sec": m.TxBps, "uptime_seconds": m.Uptime, "online_users": m.Online}
		}
		out = append(out, d)
	}
	return out, nil
}

func (s *Server) v1Nodes(r *http.Request, c *v1Ctx) (any, error) {
	items, err := s.v1NodeList(r)
	if err != nil {
		return nil, err
	}
	return map[string]any{"items": items, "total": len(items)}, nil
}

func (s *Server) v1Node(r *http.Request, c *v1Ctx) (any, error) {
	id, err := v1ID(r, "id")
	if err != nil {
		return nil, err
	}
	items, err := s.v1NodeList(r)
	if err != nil {
		return nil, err
	}
	for _, n := range items {
		if n["id"] == id {
			return n, nil
		}
	}
	return nil, notFoundErr("node")
}

func (s *Server) v1Overview(r *http.Request, c *v1Ctx) (any, error) {
	o, err := s.svc.Overview(r.Context())
	if err != nil {
		return nil, err
	}
	nodes, err := s.v1NodeList(r)
	if err != nil {
		return nil, err
	}
	up := 0
	for _, n := range nodes {
		if n["online"] == true {
			up++
		}
	}
	total := 0
	for _, n := range o.UsersByStatus {
		total += n
	}
	top := make([]map[string]any, 0, len(o.TopUsers))
	for _, t := range o.TopUsers {
		top = append(top, map[string]any{"id": t.UserID, "username": t.Username, "bytes": t.Bytes})
	}
	return map[string]any{
		"users": map[string]int{"total": total, "active": o.UsersByStatus[store.StatusActive], "limited": o.UsersByStatus[store.StatusLimited],
			"expired": o.UsersByStatus[store.StatusExpired], "disabled": o.UsersByStatus[store.StatusDisabled]},
		"online_now": o.OnlineNow, "today_bytes": o.TodayBytes, "last_30_days_bytes": o.MonthBytes,
		"nodes": map[string]int{"total": len(nodes), "online": up}, "top_users_30_days": top,
	}, nil
}

// ---- events ----

// v1Events is the feed of what happened: poll it with the cursor of the previous answer. A
// webhook delivers the same events as they happen; this is for catching up and for those who
// cannot receive calls.
func (s *Server) v1Events(r *http.Request, c *v1Ctx) (any, error) {
	ctx, q := r.Context(), r.URL.Query()
	limit, _, err := v1Page(r, 100, 1000)
	if err != nil {
		return nil, err
	}
	var types []string
	for _, t := range strings.Split(q.Get("types"), ",") {
		if t = strings.TrimSpace(t); t != "" {
			types = append(types, t)
		}
	}
	after := int64(0)
	switch v := strings.TrimPrefix(q.Get("after"), "evt_"); v {
	case "":
	case "now": // start from here: nothing old, the cursor of the newest event
		last, err := store.LastAPIEventID(ctx, s.svc.Store().DB)
		if err != nil {
			return nil, err
		}
		return map[string]any{"items": []any{}, "next_cursor": "evt_" + strconv.FormatInt(last, 10), "has_more": false}, nil
	default:
		if after, err = strconv.ParseInt(v, 10, 64); err != nil || after < 0 {
			return nil, apiErr(http.StatusBadRequest, "bad_parameter", "after is a cursor from a previous answer, or 'now'").on("after")
		}
	}
	evs, err := store.ListAPIEvents(ctx, s.svc.Store().DB, after, types, limit+1)
	if err != nil {
		return nil, err
	}
	more := len(evs) > limit
	if more {
		evs = evs[:limit]
	}
	items := make([]json.RawMessage, 0, len(evs))
	next := after
	for _, e := range evs {
		items = append(items, store.EventDoc(e))
		next = e.ID
	}
	return map[string]any{"items": items, "next_cursor": "evt_" + strconv.FormatInt(next, 10), "has_more": more}, nil
}
