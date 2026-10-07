package webapi

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
	"github.com/vyto4ka/vynel/internal/panel/subscription"
	"github.com/vyto4ka/vynel/internal/xrayconf"
)

func (s *Server) routes() {
	s.api.HandleFunc("POST /api/login", s.login)
	s.api.HandleFunc("POST /api/logout", s.logout)
	s.api.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, &httpError{http.StatusNotFound, "unknown API endpoint"})
	})

	s.handle("GET /api/session", s.session)
	s.handle("POST /api/account", s.account)
	s.handle("GET /api/overview", s.overview)
	s.handle("GET /api/qr", s.qr)
	s.handle("GET /api/audit", s.audit)

	s.handle("GET /api/users", s.users)
	s.handle("POST /api/users", s.createUser)
	s.handle("GET /api/users/{id}", s.user)
	s.handle("PATCH /api/users/{id}", s.updateUser)
	s.handle("POST /api/users/{id}/{action}", s.userAction)
	s.handle("DELETE /api/users/{id}", s.deleteUser)
	s.handle("DELETE /api/users/{id}/devices/{device}", s.deleteDevice)

	s.handle("GET /api/groups", s.groups)
	s.handle("POST /api/groups", s.createGroup)
	s.handle("PATCH /api/groups/{id}", s.updateGroup)
	s.handle("DELETE /api/groups/{id}", s.deleteGroup)
	s.handle("POST /api/groups/{id}/access", s.grant)
	s.handle("DELETE /api/groups/{id}/access", s.revoke)

	s.handle("GET /api/templates", s.templates)
	s.handle("POST /api/templates", s.saveTemplate)
	s.handle("PUT /api/templates/{id}", s.saveTemplate)
	s.handle("DELETE /api/templates/{id}", s.deleteTemplate)

	s.handle("GET /api/nodes", s.nodes)
	s.handle("POST /api/nodes", s.createNode)
	s.handle("PATCH /api/nodes/{id}", s.updateNode)
	s.handle("DELETE /api/nodes/{id}", s.deleteNode)
	s.handle("POST /api/nodes/{id}/token", s.nodeToken)

	s.handle("GET /api/profiles", s.profiles)
	s.handle("GET /api/profile-templates", s.profileTemplates)
	s.handle("POST /api/profiles", s.createProfile)
	s.handle("PATCH /api/profiles/{id}", s.updateProfile)
	s.handle("DELETE /api/profiles/{id}", s.deleteProfile)

	s.handle("POST /api/inbounds", s.attachInbound)
	s.handle("PATCH /api/inbounds/{id}", s.updateInbound)
	s.handle("DELETE /api/inbounds/{id}", s.detachInbound)
	s.handle("GET /api/inbounds/{id}/config", s.inboundConfig)
	s.editorRoutes()

	s.subscriptionRoutes()
	s.botRoutes()

	s.handle("GET /api/settings", s.settings)
	s.handle("PUT /api/settings", s.setSetting)
}

// ---- DTOs ----

// User is a user as the UI sees it.
type User struct {
	ID            int64   `json:"id"`
	Username      string  `json:"username"`
	Status        string  `json:"status"`
	Enabled       bool    `json:"enabled"`
	ExpireAt      *int64  `json:"expireAt"`
	TrafficLimit  *int64  `json:"trafficLimit"`
	TrafficUsed   int64   `json:"trafficUsed"`
	LifetimeUsed  int64   `json:"lifetimeUsed"`
	ResetStrategy string  `json:"resetStrategy"`
	HWIDLimit     *int64  `json:"hwidLimit"`
	ClientType    string  `json:"clientType"`
	Note          string  `json:"note"`
	OnlineAt      *int64  `json:"onlineAt"`
	CreatedAt     int64   `json:"createdAt"`
	SubLastAt     *int64  `json:"subLastAt"`
	SubLastUA     string  `json:"subLastUA"`
	GroupIDs      []int64 `json:"groupIds"`
	TemplateID    *int64  `json:"templateId"`
}

func userDTO(u *store.User, groups []int64) User {
	if groups == nil {
		groups = []int64{}
	}
	return User{ID: u.ID, Username: u.Username, Status: u.Status, Enabled: !u.Disabled, ExpireAt: u.ExpireAt,
		TrafficLimit: u.TrafficLimitBytes, TrafficUsed: u.TrafficUsedBytes, LifetimeUsed: u.LifetimeUsedBytes,
		ResetStrategy: u.ResetStrategy, HWIDLimit: u.HWIDLimit, ClientType: u.ClientType, Note: u.Note, OnlineAt: u.OnlineAt,
		CreatedAt: u.CreatedAt, SubLastAt: u.SubLastAt, SubLastUA: u.SubLastUA, GroupIDs: groups, TemplateID: u.TemplateID}
}

// Node is a node with live numbers.
type Node struct {
	ID           int64     `json:"id"`
	Code         string    `json:"code"`
	Name         string    `json:"name"`
	Country      string    `json:"country"`
	Flag         string    `json:"flag"`
	Domain       string    `json:"domain"`
	Local        bool      `json:"local"`
	Enabled      bool      `json:"enabled"`
	State        string    `json:"state"`
	LastSeenAt   *int64    `json:"lastSeenAt"`
	XrayVersion  string    `json:"xrayVersion"`
	AgentVersion string    `json:"agentVersion"`
	CaddyVersion string    `json:"caddyVersion"`
	Problems     []string  `json:"problems"`
	TodayBytes   int64     `json:"todayBytes"`
	Metrics      *Metrics  `json:"metrics"`
	Inbounds     []Inbound `json:"inbounds"`
	Addresses    []Address `json:"addresses"`
}

// Metrics are the latest host numbers of a node.
type Metrics struct {
	TS       int64   `json:"ts"`
	CPU      float64 `json:"cpu"`
	MemUsed  int64   `json:"memUsed"`
	MemTotal int64   `json:"memTotal"`
	Load1    float64 `json:"load1"`
	RxBps    int64   `json:"rxBps"`
	TxBps    int64   `json:"txBps"`
	Uptime   int64   `json:"uptime"`
	Online   int64   `json:"online"`
}

// Address is an IP of a node.
type Address struct {
	ID          int64  `json:"id"`
	IP          string `json:"ip"`
	Interface   string `json:"interface"`
	OnInterface bool   `json:"onInterface"`
	Primary     bool   `json:"primary"`
}

// Inbound is a profile on a node.
type Inbound struct {
	ID           int64          `json:"id"`
	NodeID       int64          `json:"nodeId"`
	ProfileID    int64          `json:"profileId"`
	ProfileName  string         `json:"profileName"`
	Tag          string         `json:"tag"`
	Listen       string         `json:"listen"`
	Port         int            `json:"port"`
	Enabled      bool           `json:"enabled"`
	Values       map[string]any `json:"values"`
	Host         *Host          `json:"host"`
	HostOverride map[string]any `json:"hostOverride"`
	Error        string         `json:"error,omitempty"`
}

// Host is a connection point.
type Host struct {
	Remark      string `json:"remark"`
	Address     string `json:"address"`
	Port        int    `json:"port"`
	Network     string `json:"network"`
	Security    string `json:"security"`
	SNI         string `json:"sni"`
	Fingerprint string `json:"fingerprint"`
	PublicKey   string `json:"publicKey"`
	ShortID     string `json:"shortId"`
	Hidden      bool   `json:"hidden"`
}

func hostDTO(h service.Host) *Host {
	return &Host{Remark: h.Remark, Address: h.Address, Port: h.Port, Network: h.Network, Security: h.Security, SNI: h.SNI,
		Fingerprint: h.Fingerprint, PublicKey: h.PublicKey, ShortID: h.ShortID, Hidden: h.Hidden}
}

// ---- session, overview ----

func (s *Server) session(r *http.Request) (any, error) {
	ctx := r.Context()
	login, _ := s.svc.Setting(ctx, service.SettingWebLogin, "")
	url, _ := s.svc.WebURL(ctx)
	return map[string]any{"login": login, "version": s.cfg.Version, "webUrl": url}, nil
}

func (s *Server) account(r *http.Request) (any, error) {
	var in struct {
		Current  string `json:"current"`
		Login    string `json:"login"`
		Password string `json:"password"`
		Generate bool   `json:"generate"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	ctx := r.Context()
	cur, _ := s.svc.Setting(ctx, service.SettingWebLogin, "")
	if err := s.svc.CheckAdmin(ctx, cur, in.Current); err != nil {
		return nil, badRequest("текущий пароль неверный")
	}
	pw := in.Password
	if in.Generate || pw == "" {
		var err error
		if pw, err = s.svc.SetAdminCredentials(ctx, actor(r), in.Login); err != nil {
			return nil, err
		}
	} else if err := s.svc.SetAdminPassword(ctx, actor(r), in.Login, pw); err != nil {
		return nil, err
	}
	login, _ := s.svc.Setting(ctx, service.SettingWebLogin, "")
	// The session key depends on the password: hand out a fresh cookie so this browser stays in.
	w := r.Context().Value(writerKey{}).(http.ResponseWriter)
	if err := s.setCookie(w, r, login); err != nil {
		return nil, err
	}
	return map[string]string{"login": login, "password": pw}, nil
}

type writerKey struct{}

func (s *Server) overview(r *http.Request) (any, error) {
	ctx := r.Context()
	o, err := s.svc.Overview(ctx)
	if err != nil {
		return nil, err
	}
	daily, err := s.svc.TrafficByDay(ctx, 0, 30)
	if err != nil {
		return nil, err
	}
	nodes, err := s.nodeList(r, false)
	if err != nil {
		return nil, err
	}
	total := 0
	for _, n := range o.UsersByStatus {
		total += n
	}
	top := make([]map[string]any, 0, len(o.TopUsers))
	for _, t := range o.TopUsers {
		top = append(top, map[string]any{"id": t.UserID, "username": t.Username, "bytes": t.Bytes})
	}
	url, _ := s.svc.WebURL(ctx)
	return map[string]any{
		"users": map[string]int{"total": total, "active": o.UsersByStatus[store.StatusActive], "limited": o.UsersByStatus[store.StatusLimited],
			"expired": o.UsersByStatus[store.StatusExpired], "disabled": o.UsersByStatus[store.StatusDisabled]},
		"onlineNow": o.OnlineNow, "todayBytes": o.TodayBytes, "monthBytes": o.MonthBytes, "topUsers": top,
		"daily": dailyDTO(daily), "nodes": nodes, "webUrl": url, "version": s.cfg.Version,
	}, nil
}

func dailyDTO(d []service.DayTraffic) []map[string]int64 {
	out := make([]map[string]int64, 0, len(d))
	for _, x := range d {
		out = append(out, map[string]int64{"day": x.Day, "up": x.Up, "down": x.Down})
	}
	return out
}

func (s *Server) qr(r *http.Request) (any, error) {
	text := r.URL.Query().Get("text")
	if text == "" || len(text) > 2000 {
		return nil, badRequest("text is required")
	}
	png, err := qrcode.Encode(text, qrcode.Medium, 384)
	if err != nil {
		return nil, err
	}
	return rawResponse{"image/png", png}, nil
}

func (s *Server) audit(r *http.Request) (any, error) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	es, err := s.svc.Audit(r.Context(), limit)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(es))
	for _, e := range es {
		out = append(out, map[string]any{"id": e.ID, "ts": e.TS, "actor": e.Actor, "actorId": e.ActorID, "action": e.Action,
			"entity": e.Entity, "entityId": e.EntityID, "diff": e.Diff})
	}
	return out, nil
}

// ---- users ----

func (s *Server) users(r *http.Request) (any, error) {
	ctx := r.Context()
	q := r.URL.Query()
	us, err := s.svc.Users(ctx, store.UserFilter{Status: q.Get("status"), Search: q.Get("search")})
	if err != nil {
		return nil, err
	}
	out := make([]User, 0, len(us))
	for _, u := range us {
		g, err := s.svc.UserGroupIDs(ctx, u.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, userDTO(u, g))
	}
	return out, nil
}

func (s *Server) createUser(r *http.Request) (any, error) {
	var in struct {
		Username     string  `json:"username"`
		TemplateID   int64   `json:"templateId"`
		GroupIDs     []int64 `json:"groupIds"`
		Note         string  `json:"note"`
		ExpireAt     *int64  `json:"expireAt"`
		NeverExpires bool    `json:"neverExpires"`
		TrafficLimit *int64  `json:"trafficLimit"`
		HWIDLimit    *int64  `json:"hwidLimit"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	ci := service.CreateUserInput{Username: in.Username, TemplateID: in.TemplateID, GroupIDs: in.GroupIDs, Note: in.Note,
		NeverExpires: in.NeverExpires, TrafficLimitBytes: in.TrafficLimit, HWIDLimit: in.HWIDLimit}
	if in.ExpireAt != nil {
		t := time.Unix(*in.ExpireAt, 0)
		ci.ExpireAt = &t
	}
	u, err := s.svc.CreateUser(r.Context(), actor(r), ci)
	if err != nil {
		return nil, err
	}
	g, _ := s.svc.UserGroupIDs(r.Context(), u.ID)
	return userDTO(u, g), nil
}

func (s *Server) user(r *http.Request) (any, error) {
	ctx := r.Context()
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	u, err := s.svc.User(ctx, id)
	if err != nil {
		return nil, err
	}
	groups, err := s.svc.UserGroupIDs(ctx, id)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"user": userDTO(u, groups), "uuid": u.UUID}
	if url, err := s.svc.SubscriptionURL(ctx, u); err == nil {
		out["subUrl"] = url
	} else {
		out["subError"] = err.Error()
	}
	devs, err := s.svc.Devices(ctx, id)
	if err != nil {
		return nil, err
	}
	dl := make([]map[string]any, 0, len(devs))
	for _, d := range devs {
		dl = append(dl, map[string]any{"id": d.ID, "hwid": d.HWID, "platform": d.Platform, "osVersion": d.OSVersion, "model": d.Model,
			"userAgent": d.UserAgent, "firstSeen": d.FirstSeen, "lastSeen": d.LastSeen, "lastIp": d.LastIP})
	}
	out["devices"] = dl
	limit, _ := s.svc.Setting(ctx, service.SettingHWIDLimit, "3")
	out["hwidDefault"], _ = strconv.Atoi(limit)
	byNode, err := s.svc.UserTrafficByNode(ctx, id, 30)
	if err != nil {
		return nil, err
	}
	bn := make([]map[string]any, 0, len(byNode))
	for _, t := range byNode {
		bn = append(bn, map[string]any{"code": t.Code, "bytes": t.Bytes})
	}
	out["trafficByNode"] = bn
	daily, err := s.svc.TrafficByDay(ctx, id, 30)
	if err != nil {
		return nil, err
	}
	out["daily"] = dailyDTO(daily)
	hosts, err := s.svc.UserHosts(ctx, id)
	if err != nil {
		return nil, err
	}
	links := make([]map[string]any, 0, len(hosts))
	for _, h := range hosts {
		links = append(links, map[string]any{"tag": h.Tag, "node": h.NodeCode, "remark": h.Remark, "hidden": h.Hidden, "link": subscription.Link(h, u.UUID)})
	}
	out["links"] = links
	return out, nil
}

func (s *Server) updateUser(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	var in struct {
		Note          *string `json:"note"`
		HWIDLimit     *int64  `json:"hwidLimit"`
		ClientType    *string `json:"clientType"`
		ResetStrategy *string `json:"resetStrategy"`
		Enabled       *bool   `json:"enabled"`
		GroupIDs      []int64 `json:"groupIds"`
		// Expiry: set expireAt, or neverExpires.
		ExpireAt     *int64 `json:"expireAt"`
		NeverExpires bool   `json:"neverExpires"`
		// Traffic limit: set trafficLimit (<=0 = unlimited) together with setTrafficLimit.
		TrafficLimit    *int64 `json:"trafficLimit"`
		SetTrafficLimit bool   `json:"setTrafficLimit"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	ctx, a := r.Context(), actor(r)
	var u *store.User
	if in.Note != nil || in.HWIDLimit != nil || in.ClientType != nil || in.ResetStrategy != nil {
		if u, err = s.svc.SetUserDetails(ctx, a, id, service.UserDetailsInput{Note: in.Note, HWIDLimit: in.HWIDLimit, ClientType: in.ClientType, ResetStrategy: in.ResetStrategy}); err != nil {
			return nil, err
		}
	}
	if in.Enabled != nil {
		if u, err = s.svc.SetUserEnabled(ctx, a, id, *in.Enabled); err != nil {
			return nil, err
		}
	}
	if in.GroupIDs != nil {
		if u, err = s.svc.SetUserGroups(ctx, a, id, in.GroupIDs); err != nil {
			return nil, err
		}
	}
	if in.NeverExpires {
		if u, err = s.svc.SetUserExpiry(ctx, a, id, nil); err != nil {
			return nil, err
		}
	} else if in.ExpireAt != nil {
		t := time.Unix(*in.ExpireAt, 0)
		if u, err = s.svc.SetUserExpiry(ctx, a, id, &t); err != nil {
			return nil, err
		}
	}
	if in.SetTrafficLimit {
		if u, err = s.svc.SetUserTrafficLimit(ctx, a, id, in.TrafficLimit); err != nil {
			return nil, err
		}
	}
	if u == nil {
		if u, err = s.svc.User(ctx, id); err != nil {
			return nil, err
		}
	}
	g, _ := s.svc.UserGroupIDs(ctx, id)
	return userDTO(u, g), nil
}

func (s *Server) userAction(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	ctx, a := r.Context(), actor(r)
	var u *store.User
	switch r.PathValue("action") {
	case "extend":
		var in struct{ Months, Days int }
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		u, err = s.svc.ExtendUser(ctx, a, id, in.Months, in.Days)
	case "reset":
		u, err = s.svc.ResetUserTraffic(ctx, a, id)
	case "reissue":
		var in struct{ Token, UUID bool }
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		if !in.Token && !in.UUID {
			in.Token = true
		}
		u, err = s.svc.ReissueUser(ctx, a, id, in.Token, in.UUID)
	default:
		return nil, &httpError{http.StatusNotFound, "unknown action"}
	}
	if err != nil {
		return nil, err
	}
	g, _ := s.svc.UserGroupIDs(ctx, id)
	return userDTO(u, g), nil
}

func (s *Server) deleteUser(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	return nil, s.svc.DeleteUser(r.Context(), actor(r), id)
}

func (s *Server) deleteDevice(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	did, err := pathID(r, "device")
	if err != nil {
		return nil, err
	}
	return nil, s.svc.DeleteDevice(r.Context(), actor(r), id, did)
}

// ---- groups ----

func (s *Server) groups(r *http.Request) (any, error) {
	ctx := r.Context()
	gs, err := s.svc.Groups(ctx)
	if err != nil {
		return nil, err
	}
	counts, err := s.svc.GroupMemberCounts(ctx)
	if err != nil {
		return nil, err
	}
	labels, err := s.refLabels(r)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(gs))
	for _, g := range gs {
		rules, err := s.svc.AccessRules(ctx, g.ID)
		if err != nil {
			return nil, err
		}
		rl := make([]map[string]any, 0, len(rules))
		for _, ru := range rules {
			label, ok := labels[ru.Kind+":"+strconv.FormatInt(ru.RefID, 10)]
			if !ok {
				label = fmt.Sprintf("удалено (#%d)", ru.RefID)
			}
			rl = append(rl, map[string]any{"kind": ru.Kind, "refId": ru.RefID, "label": label})
		}
		out = append(out, map[string]any{"id": g.ID, "name": g.Name, "description": g.Description, "users": counts[g.ID], "rules": rl})
	}
	return out, nil
}

// refLabels names access rule targets: profile names, node codes, inbound tags.
func (s *Server) refLabels(r *http.Request) (map[string]string, error) {
	ctx := r.Context()
	out := map[string]string{}
	ps, err := s.svc.Profiles(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range ps {
		out[store.AccessProfile+":"+strconv.FormatInt(p.ID, 10)] = p.Name
	}
	ns, err := s.svc.Nodes(ctx)
	if err != nil {
		return nil, err
	}
	for _, n := range ns {
		out[store.AccessNode+":"+strconv.FormatInt(n.ID, 10)] = xrayconf.CountryFlag(n.Country) + " " + n.Name + " (" + n.Code + ")"
	}
	is, err := s.svc.NodeInbounds(ctx, 0)
	if err != nil {
		return nil, err
	}
	for _, i := range is {
		out[store.AccessNodeInbound+":"+strconv.FormatInt(i.ID, 10)] = i.Tag
	}
	return out, nil
}

func (s *Server) createGroup(r *http.Request) (any, error) {
	var in struct{ Name, Description string }
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	g, err := s.svc.CreateGroup(r.Context(), actor(r), in.Name, in.Description)
	if err != nil {
		return nil, err
	}
	return map[string]int64{"id": g.ID}, nil
}

func (s *Server) updateGroup(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	var in struct{ Name, Description string }
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	_, err = s.svc.UpdateGroup(r.Context(), actor(r), id, in.Name, in.Description)
	return nil, err
}

func (s *Server) deleteGroup(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	return nil, s.svc.DeleteGroup(r.Context(), actor(r), id)
}

type accessIn struct {
	Kind  string `json:"kind"`
	RefID int64  `json:"refId"`
}

func (s *Server) grant(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	var in accessIn
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	return nil, s.svc.GrantAccess(r.Context(), actor(r), id, in.Kind, in.RefID)
}

func (s *Server) revoke(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	ref, err := strconv.ParseInt(r.URL.Query().Get("refId"), 10, 64)
	if err != nil {
		return nil, badRequest("bad refId")
	}
	return nil, s.svc.RevokeAccess(r.Context(), actor(r), id, r.URL.Query().Get("kind"), ref)
}

// ---- user templates ----

func (s *Server) templates(r *http.Request) (any, error) {
	ts, err := s.svc.UserTemplates(r.Context())
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(ts))
	for _, t := range ts {
		g := t.GroupIDs
		if g == nil {
			g = []int64{}
		}
		out = append(out, map[string]any{"id": t.ID, "name": t.Name, "isDefault": t.IsDefault, "expireMonths": t.ExpireMonths,
			"expireDays": t.ExpireDays, "trafficLimit": t.TrafficLimitBytes, "resetStrategy": t.ResetStrategy, "hwidLimit": t.HWIDLimit,
			"clientType": t.ClientType, "groupIds": g, "note": t.Note})
	}
	return out, nil
}

func (s *Server) saveTemplate(r *http.Request) (any, error) {
	var in struct {
		Name          string  `json:"name"`
		IsDefault     bool    `json:"isDefault"`
		ExpireMonths  int     `json:"expireMonths"`
		ExpireDays    int     `json:"expireDays"`
		TrafficLimit  *int64  `json:"trafficLimit"`
		ResetStrategy string  `json:"resetStrategy"`
		HWIDLimit     *int64  `json:"hwidLimit"`
		ClientType    string  `json:"clientType"`
		GroupIDs      []int64 `json:"groupIds"`
		Note          string  `json:"note"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	ti := service.UserTemplateInput{Name: in.Name, IsDefault: in.IsDefault, ExpireMonths: in.ExpireMonths, ExpireDays: in.ExpireDays,
		TrafficLimitBytes: in.TrafficLimit, ResetStrategy: in.ResetStrategy, HWIDLimit: in.HWIDLimit, ClientType: in.ClientType,
		GroupIDs: in.GroupIDs, Note: in.Note}
	if r.PathValue("id") == "" {
		_, err := s.svc.CreateUserTemplate(r.Context(), actor(r), ti)
		return nil, err
	}
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	_, err = s.svc.UpdateUserTemplate(r.Context(), actor(r), id, ti)
	return nil, err
}

func (s *Server) deleteTemplate(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	return nil, s.svc.DeleteUserTemplate(r.Context(), actor(r), id)
}

// ---- nodes ----

func (s *Server) nodes(r *http.Request) (any, error) { return s.nodeList(r, true) }

func (s *Server) nodeList(r *http.Request, details bool) ([]Node, error) {
	ctx := r.Context()
	sts, err := s.svc.NodeStatuses(ctx, s.cfg.Connected)
	if err != nil {
		return nil, err
	}
	var profiles map[int64]string
	if details {
		ps, err := s.svc.Profiles(ctx)
		if err != nil {
			return nil, err
		}
		profiles = map[int64]string{}
		for _, p := range ps {
			profiles[p.ID] = p.Name
		}
	}
	out := make([]Node, 0, len(sts))
	for _, ns := range sts {
		n := ns.Node
		d := Node{ID: n.ID, Code: n.Code, Name: n.Name, Country: n.Country, Flag: xrayconf.CountryFlag(n.Country), Domain: n.Domain,
			Local: n.Local, Enabled: n.Enabled, State: ns.State(), LastSeenAt: n.LastSeenAt, XrayVersion: n.XrayVersion,
			AgentVersion: n.AgentVersion, CaddyVersion: n.CaddyVersion, TodayBytes: ns.TodayBytes, Problems: []string{},
			Inbounds: []Inbound{}, Addresses: []Address{}}
		if n.LastError != "" {
			d.Problems = append(d.Problems, n.LastError)
		}
		d.Problems = append(d.Problems, n.Warnings...)
		if m := ns.Metrics; m != nil {
			d.Metrics = &Metrics{TS: m.TS, CPU: m.CPU, MemUsed: m.MemUsed, MemTotal: m.MemTotal, Load1: m.Load1, RxBps: m.RxBps,
				TxBps: m.TxBps, Uptime: m.Uptime, Online: m.Online}
		}
		if details {
			nis, err := s.svc.NodeInbounds(ctx, n.ID)
			if err != nil {
				return nil, err
			}
			for _, ni := range nis {
				d.Inbounds = append(d.Inbounds, s.inboundDTO(r, ni, profiles))
			}
			addrs, err := s.svc.Addresses(ctx, n.ID)
			if err != nil {
				return nil, err
			}
			for _, a := range addrs {
				d.Addresses = append(d.Addresses, Address{ID: a.ID, IP: a.IP, Interface: a.Interface, OnInterface: a.OnInterface, Primary: a.IsPrimary})
			}
		}
		out = append(out, d)
	}
	return out, nil
}

func (s *Server) inboundDTO(r *http.Request, ni *store.NodeInbound, profiles map[int64]string) Inbound {
	d := Inbound{ID: ni.ID, NodeID: ni.NodeID, ProfileID: ni.ProfileID, ProfileName: profiles[ni.ProfileID], Tag: ni.Tag,
		Enabled: ni.Enabled, Values: publicValues(ni.Values), HostOverride: ni.Host}
	if d.HostOverride == nil {
		d.HostOverride = map[string]any{}
	}
	rendered, err := s.svc.RenderNodeInbound(r.Context(), ni.ID)
	if err != nil {
		d.Error = err.Error()
		return d
	}
	d.Listen, _ = rendered.Inbound["listen"].(string)
	if d.Listen == "" {
		d.Listen = "0.0.0.0"
	}
	d.Port, _ = rendered.Inbound["port"].(int)
	if h, err := s.svc.HostFor(r.Context(), ni); err == nil {
		d.Host = hostDTO(h)
	} else {
		d.Error = err.Error()
	}
	return d
}

// publicValues hides private keys from the UI.
func publicValues(v map[string]any) map[string]any {
	out := map[string]any{}
	for k, x := range v {
		if strings.Contains(k, "PRIVATE") || strings.Contains(k, "SECRET") {
			out[k] = "•••"
			continue
		}
		out[k] = x
	}
	return out
}

type nodeIn struct {
	Name    string `json:"name"`
	Country string `json:"country"`
	Domain  string `json:"domain"`
	Code    string `json:"code"`
	// Attach these profiles to a new node right away.
	ProfileIDs []int64 `json:"profileIds"`
}

func (s *Server) createNode(r *http.Request) (any, error) {
	var in nodeIn
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	ctx, a := r.Context(), actor(r)
	n, secret, err := s.svc.CreateNode(ctx, a, service.NodeInput{Name: in.Name, Country: in.Country, Domain: in.Domain, Code: in.Code})
	if err != nil {
		return nil, err
	}
	var warnings []string
	for _, pid := range in.ProfileIDs {
		if _, err := s.svc.AttachProfile(ctx, a, service.AttachInput{NodeID: n.ID, ProfileID: pid}); err != nil {
			warnings = append(warnings, err.Error())
		}
	}
	return s.joinInfo(r, n, secret, warnings)
}

func (s *Server) joinInfo(r *http.Request, n *store.Node, secret string, warnings []string) (any, error) {
	out := map[string]any{"code": n.Code, "id": n.ID, "warnings": warnings, "ttl": fmt.Sprintf("%.0f ч", service.InstallTokenTTL.Hours())}
	if s.cfg.CAFingerprint == nil {
		out["error"] = "CA is not available"
		return out, nil
	}
	tok, err := s.svc.JoinToken(r.Context(), s.cfg.CAFingerprint(), secret)
	if err != nil {
		out["error"] = err.Error()
		return out, nil
	}
	out["token"] = tok
	out["command"] = s.svc.NodeInstallCommand(r.Context(), tok)
	return out, nil
}

func (s *Server) updateNode(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	var in struct {
		Name    string  `json:"name"`
		Country string  `json:"country"`
		Domain  *string `json:"domain"`
		Enabled *bool   `json:"enabled"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	n, err := s.svc.Node(r.Context(), id)
	if err != nil {
		return nil, err
	}
	enabled, domain := n.Enabled, n.Domain
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	if in.Domain != nil {
		domain = *in.Domain
	}
	_, err = s.svc.UpdateNode(r.Context(), actor(r), id, service.NodeInput{Name: in.Name, Country: in.Country, Domain: domain}, enabled)
	return nil, err
}

func (s *Server) deleteNode(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	n, err := s.svc.Node(r.Context(), id)
	if err != nil {
		return nil, err
	}
	if n.Local {
		return nil, badRequest("локальную ноду (на сервере панели) удалить нельзя, её можно только выключить")
	}
	return nil, s.svc.DeleteNode(r.Context(), actor(r), id)
}

func (s *Server) nodeToken(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	n, err := s.svc.Node(r.Context(), id)
	if err != nil {
		return nil, err
	}
	if n.Local {
		return nil, badRequest("локальной ноде токен не нужен")
	}
	secret, err := s.svc.ReissueInstallToken(r.Context(), actor(r), id)
	if err != nil {
		return nil, err
	}
	return s.joinInfo(r, n, secret, nil)
}

// ---- profiles and inbounds ----

func (s *Server) profiles(r *http.Request) (any, error) {
	ctx := r.Context()
	ps, err := s.svc.Profiles(ctx)
	if err != nil {
		return nil, err
	}
	nis, err := s.svc.NodeInbounds(ctx, 0)
	if err != nil {
		return nil, err
	}
	tpls, err := s.svc.ProfileTemplates(ctx)
	if err != nil {
		return nil, err
	}
	titles := map[string]string{}
	for _, t := range tpls {
		titles[t.ID] = t.Title
	}
	out := make([]map[string]any, 0, len(ps))
	for _, p := range ps {
		var tags []string
		for _, ni := range nis {
			if ni.ProfileID == p.ID {
				tags = append(tags, ni.Tag)
			}
		}
		if tags == nil {
			tags = []string{}
		}
		over := p.Override
		if over == nil {
			over = map[string]any{}
		}
		out = append(out, map[string]any{"id": p.ID, "name": p.Name, "templateId": p.TemplateID, "templateTitle": titles[p.TemplateID],
			"values": publicValues(p.Values), "override": over, "tagPattern": p.TagPattern, "remarkPattern": p.RemarkPattern, "inbounds": tags})
	}
	return out, nil
}

type profileIn struct {
	Name       string         `json:"name"`
	TemplateID string         `json:"templateId"`
	Values     map[string]any `json:"values"`
	Override   map[string]any `json:"override"`
	GroupIDs   []int64        `json:"groupIds"`
}

func (s *Server) createProfile(r *http.Request) (any, error) {
	var in profileIn
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	ctx, a := r.Context(), actor(r)
	p, err := s.svc.CreateProfile(ctx, a, service.ProfileInput{Name: in.Name, TemplateID: in.TemplateID, Values: cleanValues(in.Values), Override: in.Override})
	if err != nil {
		return nil, err
	}
	for _, g := range in.GroupIDs {
		if err := s.svc.GrantAccess(ctx, a, g, store.AccessProfile, p.ID); err != nil {
			return nil, err
		}
	}
	return map[string]int64{"id": p.ID}, nil
}

// cleanValues turns "" into nil (back to the default) and numeric strings into numbers.
func cleanValues(v map[string]any) map[string]any {
	if v == nil {
		return nil
	}
	out := map[string]any{}
	for k, x := range v {
		if str, ok := x.(string); ok {
			if str == "" {
				out[k] = nil
				continue
			}
			if n, err := strconv.Atoi(str); err == nil {
				out[k] = n
				continue
			}
		}
		if f, ok := x.(float64); ok && f == float64(int(f)) {
			out[k] = int(f)
			continue
		}
		out[k] = x
	}
	return out
}

func (s *Server) deleteProfile(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	return nil, s.svc.DeleteProfile(r.Context(), actor(r), id)
}

func (s *Server) attachInbound(r *http.Request) (any, error) {
	var in struct {
		NodeID    int64          `json:"nodeId"`
		ProfileID int64          `json:"profileId"`
		Port      int            `json:"port"`
		Values    map[string]any `json:"values"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	ni, err := s.svc.AttachProfile(r.Context(), actor(r), service.AttachInput{NodeID: in.NodeID, ProfileID: in.ProfileID, PortOverride: in.Port, Values: cleanValues(in.Values)})
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": ni.ID, "tag": ni.Tag}, nil
}

func (s *Server) detachInbound(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	return nil, s.svc.DetachInbound(r.Context(), actor(r), id)
}

// ---- settings ----

func (s *Server) settings(r *http.Request) (any, error) {
	ctx := r.Context()
	stored, err := s.svc.Settings(ctx)
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	out := make([]map[string]any, 0, len(settingDefs)+len(stored))
	for _, d := range settingDefs {
		known[d.Key] = true
		v, set := stored[d.Key]
		out = append(out, map[string]any{"key": d.Key, "section": d.Section, "title": d.Title, "help": d.Help, "type": d.Type,
			"options": d.Options, "default": d.Default, "value": v, "set": set})
	}
	var extra []string
	for k := range stored {
		if !known[k] && !hiddenSettings[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	for _, k := range extra {
		out = append(out, map[string]any{"key": k, "section": "Прочее", "title": k, "type": "string", "value": stored[k], "set": true})
	}
	return out, nil
}

func (s *Server) setSetting(r *http.Request) (any, error) {
	var in struct{ Key, Value string }
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	in.Key = strings.TrimSpace(in.Key)
	if in.Key == "" || service.IsSecretSetting(in.Key) || in.Key == service.SettingWebLogin {
		return nil, badRequest("эту настройку нельзя менять отсюда")
	}
	for _, d := range settingDefs {
		if d.Key != in.Key {
			continue
		}
		switch d.Type {
		case "bool":
			if in.Value != "" && in.Value != "true" && in.Value != "false" {
				return nil, badRequest("ожидается true или false")
			}
		case "int":
			if in.Value != "" {
				if _, err := strconv.Atoi(in.Value); err != nil {
					return nil, badRequest("ожидается число")
				}
			}
		}
	}
	if in.Key == service.SettingWebPath {
		p := strings.Trim(in.Value, "/ ")
		if len(p) < 6 || strings.ContainsAny(p, "/?#% ") {
			return nil, badRequest("секретный путь: минимум 6 символов, без / ? # % и пробелов")
		}
		in.Value = "/" + p + "/"
	}
	if err := s.svc.SetSetting(r.Context(), actor(r), in.Key, in.Value); err != nil {
		return nil, err
	}
	url, _ := s.svc.WebURL(r.Context())
	return map[string]string{"webUrl": url}, nil
}
