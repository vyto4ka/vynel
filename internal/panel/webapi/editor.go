package webapi

import (
	"context"
	"net/http"

	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
	"github.com/vyto4ka/vynel/internal/xrayconf"
)

// Editors of profile templates, profiles and node inbounds, with previews of what nodes get.

func (s *Server) editorRoutes() {
	s.handle("GET /api/profile-templates/reference", s.templateReference)
	s.handle("POST /api/profile-templates/check", s.checkTemplate)
	s.handle("POST /api/profile-templates", s.saveProfileTemplate)
	s.handle("PUT /api/profile-templates/{id}", s.saveProfileTemplate)
	s.handle("DELETE /api/profile-templates/{id}", s.deleteProfileTemplate)
	s.handle("GET /api/profiles/{id}", s.profile)
	s.handle("POST /api/profiles/{id}/preview", s.previewProfile)
	s.handle("GET /api/inbounds/{id}", s.inbound)
	s.handle("POST /api/inbounds/{id}/preview", s.previewInbound)
}

const masked = "•••"

// maskSecrets hides the values of secret variables (Reality private keys…) in a rendered tree.
func maskSecrets(r *xrayconf.RenderedInbound) map[string]any {
	secrets := map[string]bool{}
	if r.Template != nil {
		for _, v := range r.Template.Variables {
			if v.Secret {
				if s, ok := r.Values[v.Name].(string); ok && s != "" {
					secrets[s] = true
				}
			}
		}
	}
	var walk func(any) any
	walk = func(x any) any {
		switch t := x.(type) {
		case map[string]any:
			out := make(map[string]any, len(t))
			for k, v := range t {
				out[k] = walk(v)
			}
			return out
		case []any:
			out := make([]any, len(t))
			for i, v := range t {
				out[i] = walk(v)
			}
			return out
		case string:
			if secrets[t] {
				return masked
			}
		}
		return x
	}
	m, _ := walk(r.Inbound).(map[string]any)
	if ss, ok := m["streamSettings"].(map[string]any); ok {
		if rs, ok := ss["realitySettings"].(map[string]any); ok {
			if _, ok := rs["privateKey"]; ok {
				rs["privateKey"] = masked
			}
		}
	}
	return m
}

// secretNames lists the template's secret variables.
func secretNames(t *xrayconf.Template) map[string]bool {
	out := map[string]bool{}
	if t != nil {
		for _, v := range t.Variables {
			if v.Secret {
				out[v.Name] = true
			}
		}
	}
	return out
}

func maskValues(vals map[string]any, secret map[string]bool) map[string]any {
	out := map[string]any{}
	for k, v := range vals {
		if secret[k] {
			out[k] = masked
		} else {
			out[k] = v
		}
	}
	return out
}

// dropMasked removes values the UI sent back unchanged as "•••".
func dropMasked(v map[string]any) map[string]any {
	for k, x := range v {
		if x == masked {
			delete(v, k)
		}
	}
	return v
}

// ---- templates ----

func (s *Server) profileTemplates(r *http.Request) (any, error) {
	return s.svc.ProfileTemplates(r.Context())
}

func (s *Server) templateReference(*http.Request) (any, error) {
	return map[string]any{"reference": xrayconf.Reference(), "sample": xrayconf.SampleContext}, nil
}

func (s *Server) checkTemplate(r *http.Request) (any, error) {
	var in struct {
		Source string `json:"source"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	t, rendered, err := s.svc.CheckTemplate(in.Source)
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}, nil
	}
	return map[string]any{"ok": true, "id": t.ID, "title": t.Title, "variables": t.Variables, "sample": maskSecrets(rendered)}, nil
}

func (s *Server) saveProfileTemplate(r *http.Request) (any, error) {
	var in struct {
		Source string `json:"source"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	t, err := s.svc.SaveTemplate(r.Context(), actor(r), r.PathValue("id"), in.Source)
	if err != nil {
		return nil, err
	}
	return map[string]string{"id": t.ID}, nil
}

func (s *Server) deleteProfileTemplate(r *http.Request) (any, error) {
	return nil, s.svc.DeleteTemplate(r.Context(), actor(r), r.PathValue("id"))
}

// ---- profiles ----

type profileEdit struct {
	Name           string         `json:"name"`
	Values         map[string]any `json:"values"`
	Override       map[string]any `json:"override"`
	Inbound        map[string]any `json:"inboundSource"` // the full inbound JSON as edited
	Host           map[string]any `json:"hostSource"`    // the connection point JSON as edited
	TagPattern     string         `json:"tagPattern"`
	RemarkPattern  string         `json:"remarkPattern"`
	RegenerateKeys bool           `json:"regenerateKeys"`
}

func (e profileEdit) input() service.ProfileInput {
	return service.ProfileInput{Name: e.Name, Values: cleanValues(dropMasked(e.Values)), Override: e.Override,
		Inbound: e.Inbound, Host: e.Host,
		TagPattern: e.TagPattern, RemarkPattern: e.RemarkPattern, RegenerateKeys: e.RegenerateKeys}
}

func (s *Server) templateOf(ctx context.Context, id string) *xrayconf.Template {
	ts, _ := s.svc.ProfileTemplates(ctx)
	for _, t := range ts {
		if t.ID == id {
			return t.Template
		}
	}
	return nil
}

func (s *Server) profileDTO(ctx context.Context, p *store.Profile) map[string]any {
	t := s.templateOf(ctx, p.TemplateID)
	over := p.Override
	if over == nil {
		over = map[string]any{}
	}
	out := map[string]any{"id": p.ID, "name": p.Name, "templateId": p.TemplateID, "values": maskValues(p.Values, secretNames(t)),
		"override": over, "tagPattern": p.TagPattern, "remarkPattern": p.RemarkPattern, "templateVersion": p.TemplateVersion}
	if t != nil {
		out["template"] = map[string]any{"id": t.ID, "title": t.Title, "summary": t.Summary, "version": t.Version, "variables": t.Variables,
			"custom": t.Custom, "tagPattern": t.TagPattern, "remarkPattern": t.RemarkPattern,
			"inboundSource": t.SourceInbound(), "hostSource": t.SourceHost()}
		in, host := service.ProfileSources(t, p)
		out["inboundSource"], out["hostSource"] = in, host
		out["ownInbound"], out["ownHost"] = p.Inbound != nil || len(p.Override) > 0, p.Host != nil
	}
	return out
}

func (s *Server) profile(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	ps, err := s.svc.Profiles(r.Context())
	if err != nil {
		return nil, err
	}
	for _, p := range ps {
		if p.ID == id {
			return s.profileDTO(r.Context(), p), nil
		}
	}
	return nil, service.ErrNotFound
}

func (s *Server) updateProfile(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	var in profileEdit
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	p, err := s.svc.UpdateProfile(r.Context(), actor(r), id, in.input())
	if err != nil {
		return nil, err
	}
	return s.profileDTO(r.Context(), p), nil
}

func (s *Server) previewProfile(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	var in profileEdit
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	_, pvs, err := s.svc.PreviewProfile(r.Context(), id, in.input())
	if err != nil {
		return map[string]any{"error": err.Error(), "inbounds": []any{}}, nil
	}
	out := make([]map[string]any, 0, len(pvs))
	for _, pv := range pvs {
		item := map[string]any{"id": pv.Inbound.ID, "tag": pv.Inbound.Tag}
		if pv.Node != nil {
			item["node"] = xrayconf.CountryFlag(pv.Node.Country) + " " + pv.Node.Name + " (" + pv.Node.Code + ")"
		}
		if pv.Err != nil {
			item["error"] = pv.Err.Error()
		} else {
			item["inbound"] = maskSecrets(pv.Rendered)
			if t := pv.Rendered.Template; t != nil && t.Host != nil {
				vals := map[string]any{}
				for k, v := range pv.Rendered.Values {
					vals[k] = v
				}
				vals["INBOUND_PORT"] = pv.Rendered.Inbound["port"]
				if host, err := xrayconf.ExpandTree(t.Host, vals); err == nil {
					item["host"] = host
				} else {
					item["hostError"] = err.Error()
				}
			}
		}
		out = append(out, item)
	}
	return map[string]any{"inbounds": out}, nil
}

// ---- node inbounds ----

type inboundEdit struct {
	Enabled         *bool          `json:"enabled"`
	Port            *int           `json:"port"`
	Values          map[string]any `json:"values"`
	Override        map[string]any `json:"override"`
	Tag             string         `json:"tag"`
	ListenAddressID *int64         `json:"listenAddressId"`
	EgressAddressID *int64         `json:"egressAddressId"`
	ClearAddresses  bool           `json:"clearAddresses"`
	RegenerateKeys  bool           `json:"regenerateKeys"`
	Host            map[string]any `json:"host"`
}

func (e inboundEdit) input() service.NodeInboundInput {
	return service.NodeInboundInput{Enabled: e.Enabled, PortOverride: e.Port, Values: cleanValues(dropMasked(e.Values)), Override: e.Override,
		Tag: e.Tag, ListenAddressID: e.ListenAddressID, EgressAddressID: e.EgressAddressID, ClearAddresses: e.ClearAddresses,
		RegenerateKeys: e.RegenerateKeys}
}

func (s *Server) inbound(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	ctx := r.Context()
	nis, err := s.svc.NodeInbounds(ctx, 0)
	if err != nil {
		return nil, err
	}
	var ni *store.NodeInbound
	for _, x := range nis {
		if x.ID == id {
			ni = x
		}
	}
	if ni == nil {
		return nil, service.ErrNotFound
	}
	ps, err := s.svc.Profiles(ctx)
	if err != nil {
		return nil, err
	}
	var prof *store.Profile
	for _, p := range ps {
		if p.ID == ni.ProfileID {
			prof = p
		}
	}
	if prof == nil {
		return nil, service.ErrNotFound
	}
	t := s.templateOf(ctx, prof.TemplateID)
	addrs, err := s.svc.Addresses(ctx, ni.NodeID)
	if err != nil {
		return nil, err
	}
	al := make([]Address, 0, len(addrs))
	for _, a := range addrs {
		al = append(al, Address{ID: a.ID, IP: a.IP, Interface: a.Interface, OnInterface: a.OnInterface, Primary: a.IsPrimary})
	}
	over := ni.Override
	if over == nil {
		over = map[string]any{}
	}
	out := map[string]any{"id": ni.ID, "tag": ni.Tag, "nodeId": ni.NodeID, "enabled": ni.Enabled, "port": ni.PortOverride,
		"listenAddressId": ni.ListenAddressID, "egressAddressId": ni.EgressAddressID, "values": maskValues(ni.Values, secretNames(t)),
		"override": over, "profile": map[string]any{"id": prof.ID, "name": prof.Name}, "addresses": al}
	if t != nil {
		out["variables"] = t.Variables
	}
	if rendered, err := s.svc.RenderNodeInbound(ctx, id); err == nil {
		out["rendered"] = maskSecrets(rendered)
	} else {
		out["error"] = err.Error()
	}
	return out, nil
}

func (s *Server) updateInbound(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	var in inboundEdit
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	ctx, a := r.Context(), actor(r)
	ii := in.input()
	if ii.Enabled != nil || ii.PortOverride != nil || len(ii.Values) > 0 || ii.Override != nil || ii.Tag != "" ||
		ii.ListenAddressID != nil || ii.EgressAddressID != nil || ii.ClearAddresses || ii.RegenerateKeys {
		if _, err := s.svc.UpdateNodeInbound(ctx, a, id, ii); err != nil {
			return nil, err
		}
	}
	if in.Host != nil {
		if err := s.svc.SetHostOverride(ctx, a, id, in.Host); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

func (s *Server) previewInbound(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	var in inboundEdit
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	_, rendered, err := s.svc.PreviewNodeInbound(r.Context(), id, in.input())
	if err != nil {
		return map[string]any{"error": err.Error()}, nil
	}
	return map[string]any{"inbound": maskSecrets(rendered)}, nil
}

func (s *Server) inboundConfig(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	rendered, err := s.svc.RenderNodeInbound(r.Context(), id)
	if err != nil {
		return nil, err
	}
	b, err := xrayconf.MarshalIndent(maskSecrets(rendered))
	if err != nil {
		return nil, err
	}
	return rawResponse{"application/json", b}, nil
}
