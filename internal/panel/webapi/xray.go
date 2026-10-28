package webapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/xrayconf"
)

// The «Xray» page: the base config of every node as JSON, checked before it is saved.

func (s *Server) xrayRoutes() {
	s.handle("GET /api/xray/base", s.xrayBase)
	s.handle("POST /api/xray/base/check", s.xrayCheck)
	s.handle("PUT /api/xray/base", s.xraySave)
	s.handle("POST /api/xray/nodes/{id}/config", s.xrayNodeConfig)
}

func (s *Server) xrayBase(r *http.Request) (any, error) {
	info, err := s.svc.BaseConfig(r.Context())
	if err != nil {
		return nil, err
	}
	nodes, err := s.svc.Nodes(r.Context())
	if err != nil {
		return nil, err
	}
	list := make([]map[string]any, 0, len(nodes))
	for _, n := range nodes {
		list = append(list, map[string]any{"id": n.ID, "code": n.Code, "name": n.Name, "flag": xrayconf.CountryFlag(n.Country)})
	}
	return map[string]any{"json": info.JSON, "default": info.Default, "customized": info.Customized, "panelKeys": info.PanelKeys,
		"tester": s.cfg.XrayTest != nil, "nodes": list}, nil
}

type baseEdit struct {
	JSON string `json:"json"`
}

// xrayCheck builds every node's config with the edited base config, and lets Xray itself read
// them when the binary is on the panel's server. The answer is always 200: a problem is the result.
func (s *Server) xrayCheck(r *http.Request) (any, error) {
	var in baseEdit
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	chk, err := s.svc.CheckBaseConfig(r.Context(), in.JSON)
	if err != nil {
		return map[string]any{"ok": false, "error": strings.TrimPrefix(err.Error(), service.ErrInvalid.Error()+": ")}, nil
	}
	problems := chk.Problems
	rejected, tested := s.xrayTest(r.Context(), chk.Nodes)
	if rejected != "" {
		problems = append(problems, rejected)
	}
	if problems == nil {
		problems = []string{}
	}
	return map[string]any{"ok": len(problems) == 0, "problems": problems, "tested": tested, "nodes": nodeCount(chk.Nodes)}, nil
}

func (s *Server) xraySave(r *http.Request) (any, error) {
	var in baseEdit
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	if chk, err := s.svc.CheckBaseConfig(r.Context(), in.JSON); err == nil {
		if rejected, _ := s.xrayTest(r.Context(), chk.Nodes); rejected != "" {
			return nil, badRequest("Xray не принял конфиг: " + rejected)
		}
	}
	return nil, s.svc.SaveBaseConfig(r.Context(), actor(r), in.JSON)
}

// xrayTest lets Xray read the config of each node (identical ones once) and stops at the first it
// refuses. tested is false when this server has no Xray to ask.
func (s *Server) xrayTest(ctx context.Context, nodes []service.BaseCheckNode) (rejected string, tested bool) {
	if s.cfg.XrayTest == nil {
		return "", false
	}
	seen := map[string]bool{}
	for _, n := range nodes {
		if seen[string(n.Config)] {
			continue
		}
		seen[string(n.Config)] = true
		tested = true
		if err := s.cfg.XrayTest(ctx, n.Config); err != nil {
			return fmt.Sprintf("%s: %v", n.Code, err), tested
		}
	}
	return "", tested
}

// xrayNodeConfig is the full config a node runs (or would, with the base config in the request).
func (s *Server) xrayNodeConfig(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	var in baseEdit
	if r.ContentLength != 0 {
		if err := decode(r, &in); err != nil {
			return nil, err
		}
	}
	cfg, problems, origins, err := s.svc.NodeConfigPreview(r.Context(), id, in.JSON)
	if err != nil {
		return nil, err
	}
	if problems == nil {
		problems = []string{}
	}
	if origins == nil {
		origins = []service.ConfigOrigin{}
	}
	return map[string]any{"config": cfg, "problems": problems, "origins": origins}, nil
}

// nodeCount counts real nodes: with none, the check builds a stand-in config of the base alone.
func nodeCount(nodes []service.BaseCheckNode) int {
	if len(nodes) == 1 && nodes[0].ID == 0 {
		return 0
	}
	return len(nodes)
}
