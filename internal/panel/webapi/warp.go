package webapi

import (
	"net/http"

	"github.com/vyto4ka/vynel/internal/panel/service"
)

func (s *Server) warpRoutes() {
	s.handle("GET /api/nodes/{id}/warp", s.getNodeWarp)
	s.handle("PUT /api/nodes/{id}/warp", s.saveNodeWarp)
	s.handle("POST /api/nodes/{id}/warp/provision", s.provisionNodeWarp)
	s.handle("POST /api/nodes/{id}/warp/check", s.checkNodeWarp)
}

func (s *Server) getNodeWarp(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	return s.svc.NodeWarp(r.Context(), id)
}

func (s *Server) saveNodeWarp(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	var in service.NodeWarpInput
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	return s.svc.SaveNodeWarp(r.Context(), actor(r), id, in)
}

func (s *Server) provisionNodeWarp(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	var in service.NodeWarpProvisionInput
	if r.ContentLength != 0 {
		if err := decode(r, &in); err != nil {
			return nil, err
		}
	}
	return s.svc.ProvisionNodeWarp(r.Context(), actor(r), id, in)
}

func (s *Server) checkNodeWarp(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	return s.svc.CheckNodeWarp(r.Context(), id)
}
