package webapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/service"
)

func (s *Server) geoRoutes() {
	s.handle("GET /api/geo/files", s.listGeoFiles)
	s.handle("POST /api/geo/files", s.addGeoFile)
	s.handle("POST /api/geo/files/defaults", s.loadDefaultGeoFiles)
	s.handle("PATCH /api/geo/files/{id}", s.updateGeoFile)
	s.handle("POST /api/geo/files/{id}/refresh", s.refreshGeoFile)
	s.handle("DELETE /api/geo/files/{id}", s.deleteGeoFile)
	s.handle("GET /api/geo/search", s.searchGeo)
	s.handle("GET /api/geo/files/{name}/categories/{code}", s.inspectGeoCategory)
}

func (s *Server) listGeoFiles(r *http.Request) (any, error) {
	return s.svc.ListGeoFiles(r.Context())
}

func (s *Server) addGeoFile(r *http.Request) (any, error) {
	var in service.GeoFileAddInput
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	if w, ok := r.Context().Value(writerKey{}).(http.ResponseWriter); ok {
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(2 * time.Minute))
	}
	return s.svc.AddGeoFile(r.Context(), actor(r), in)
}

func (s *Server) loadDefaultGeoFiles(r *http.Request) (any, error) {
	if w, ok := r.Context().Value(writerKey{}).(http.ResponseWriter); ok {
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(3 * time.Minute))
	}
	return s.svc.LoadDefaultGeoFiles(r.Context(), actor(r))
}

func (s *Server) updateGeoFile(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	var in service.GeoFilePatchInput
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	return s.svc.UpdateGeoFile(r.Context(), actor(r), id, in)
}

func (s *Server) refreshGeoFile(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	if w, ok := r.Context().Value(writerKey{}).(http.ResponseWriter); ok {
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(2 * time.Minute))
	}
	return s.svc.RefreshGeoFile(r.Context(), actor(r), id)
}

func (s *Server) deleteGeoFile(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	return nil, s.svc.DeleteGeoFile(r.Context(), actor(r), id)
}

func (s *Server) searchGeo(r *http.Request) (any, error) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	return s.svc.SearchGeo(r.Context(), q.Get("q"), q.Get("kind"), q.Get("file"), q.Get("mode"), limit)
}

func (s *Server) inspectGeoCategory(r *http.Request) (any, error) {
	name := r.PathValue("name")
	code := r.PathValue("code")
	q := r.URL.Query()
	offset, _ := strconv.Atoi(q.Get("offset"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	return s.svc.InspectGeoCategory(r.Context(), name, code, q.Get("q"), q.Get("attr"), offset, limit)
}
