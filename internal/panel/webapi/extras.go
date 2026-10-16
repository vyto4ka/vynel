package webapi

import (
	"net/http"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
	"github.com/vyto4ka/vynel/internal/xrayconf"
)

// Cascades, merged subscriptions and who gets them (docs/USER_GUIDE.md §5.2, §5.3).
func (s *Server) extraRoutes() {
	s.handle("GET /api/cascades", s.cascades)
	s.handle("POST /api/cascades", s.saveCascade)
	s.handle("PATCH /api/cascades/{id}", s.saveCascade)
	s.handle("DELETE /api/cascades/{id}", s.deleteCascade)
	s.handle("GET /api/externals", s.externals)
	s.handle("POST /api/externals", s.saveExternal)
	s.handle("PATCH /api/externals/{id}", s.saveExternal)
	s.handle("DELETE /api/externals/{id}", s.deleteExternal)
	s.handle("POST /api/externals/{id}/refresh", s.refreshExternal)
	s.handle("GET /api/grants", s.grants)
	s.handle("POST /api/grants", s.putGrant)
	s.handle("DELETE /api/grants/{id}", s.deleteGrant)
}

func (s *Server) cascades(r *http.Request) (any, error) {
	ctx := r.Context()
	cs, err := s.svc.Cascades(ctx)
	if err != nil {
		return nil, err
	}
	label := map[int64]string{}
	if nis, err := s.svc.NodeInbounds(ctx, 0); err == nil {
		nodes, _ := s.svc.Nodes(ctx)
		for _, ni := range nis {
			label[ni.ID] = ni.Tag
			for _, n := range nodes {
				if n.ID == ni.NodeID {
					label[ni.ID] = xrayconf.CountryFlag(n.Country) + " " + n.Name + " · " + ni.Tag
				}
			}
		}
	}
	out := make([]map[string]any, 0, len(cs))
	for _, c := range cs {
		out = append(out, map[string]any{"id": c.ID, "name": c.Name, "entryInboundId": c.EntryInboundID, "exitInboundId": c.ExitInboundID,
			"entry": label[c.EntryInboundID], "exit": label[c.ExitInboundID], "remark": c.Remark, "enabled": c.Enabled,
			"title": s.svc.CascadeRemark(ctx, s.svc.Store().DB, c)})
	}
	return out, nil
}

func (s *Server) saveCascade(r *http.Request) (any, error) {
	var in struct {
		Name           string `json:"name"`
		EntryInboundID int64  `json:"entryInboundId"`
		ExitInboundID  int64  `json:"exitInboundId"`
		Remark         string `json:"remark"`
		Enabled        bool   `json:"enabled"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	ci := service.CascadeInput{Name: in.Name, EntryInboundID: in.EntryInboundID, ExitInboundID: in.ExitInboundID, Remark: in.Remark, Enabled: in.Enabled}
	if r.Method == http.MethodPost {
		c, err := s.svc.CreateCascade(r.Context(), actor(r), ci)
		if err != nil {
			return nil, err
		}
		return map[string]int64{"id": c.ID}, nil
	}
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	if _, err := s.svc.UpdateCascade(r.Context(), actor(r), id, ci); err != nil {
		return nil, err
	}
	return map[string]int64{"id": id}, nil
}

func (s *Server) deleteCascade(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	return nil, s.svc.DeleteCascade(r.Context(), actor(r), id)
}

func externalDTO(e *store.ExternalSub) map[string]any {
	servers := service.ParseLinks(e.Links)
	names := make([]string, 0, len(servers))
	for _, h := range servers {
		names = append(names, h.Remark)
	}
	return map[string]any{"id": e.ID, "name": e.Name, "url": e.URL, "enabled": e.Enabled, "fetchedAt": e.FetchedAt, "error": e.Error, "servers": names}
}

func (s *Server) externals(r *http.Request) (any, error) {
	es, err := s.svc.ExternalSubs(r.Context())
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(es))
	for _, e := range es {
		out = append(out, externalDTO(e))
	}
	return out, nil
}

func (s *Server) saveExternal(r *http.Request) (any, error) {
	var in struct {
		Name    string `json:"name"`
		URL     string `json:"url"`
		Enabled bool   `json:"enabled"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	ei := service.ExternalSubInput{Name: in.Name, URL: in.URL, Enabled: in.Enabled}
	if r.Method == http.MethodPost {
		e, err := s.svc.CreateExternalSub(r.Context(), actor(r), ei)
		if err != nil {
			return nil, err
		}
		return externalDTO(e), nil
	}
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	e, err := s.svc.UpdateExternalSub(r.Context(), actor(r), id, ei)
	if err != nil {
		return nil, err
	}
	return externalDTO(e), nil
}

func (s *Server) deleteExternal(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	return nil, s.svc.DeleteExternalSub(r.Context(), actor(r), id)
}

// refreshExternal fetches now; a failure is shown on the item, the last good servers stay.
func (s *Server) refreshExternal(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	_ = s.svc.RefreshExternalSub(r.Context(), id)
	es, err := s.svc.ExternalSubs(r.Context())
	if err != nil {
		return nil, err
	}
	for _, e := range es {
		if e.ID == id {
			return externalDTO(e), nil
		}
	}
	return nil, service.ErrNotFound
}

func (s *Server) grants(r *http.Request) (any, error) {
	gs, err := s.svc.Grants(r.Context())
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(gs))
	for _, g := range gs {
		out = append(out, map[string]any{"id": g.ID, "userId": g.UserID, "groupId": g.GroupID, "kind": g.Kind, "refId": g.RefID, "expireAt": g.ExpireAt})
	}
	return out, nil
}

func (s *Server) putGrant(r *http.Request) (any, error) {
	var in struct {
		UserID   int64  `json:"userId"`
		GroupID  int64  `json:"groupId"`
		Kind     string `json:"kind"`
		RefID    int64  `json:"refId"`
		ExpireAt *int64 `json:"expireAt"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	gi := service.GrantInput{UserID: in.UserID, GroupID: in.GroupID, Kind: in.Kind, RefID: in.RefID}
	if in.ExpireAt != nil && *in.ExpireAt > 0 {
		t := time.Unix(*in.ExpireAt, 0)
		gi.ExpireAt = &t
	}
	g, err := s.svc.PutGrant(r.Context(), actor(r), gi)
	if err != nil {
		return nil, err
	}
	return map[string]int64{"id": g.ID}, nil
}

func (s *Server) deleteGrant(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	return nil, s.svc.DeleteGrant(r.Context(), actor(r), id)
}
