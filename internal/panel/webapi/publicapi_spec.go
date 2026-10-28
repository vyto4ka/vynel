package webapi

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"strings"

	"gopkg.in/yaml.v3"
)

// openAPISpec describes the public API. A test keeps it in step with the routes.
//
//go:embed openapi.yaml
var openAPISpec string

// APISpec returns the specification with the address of the panel filled in.
func (s *Server) apiSpec(r *http.Request) string {
	return strings.ReplaceAll(openAPISpec, "{{server}}", s.apiBase(r))
}

// apiBase is the address of the public API of this panel, without a closing slash.
func (s *Server) apiBase(r *http.Request) string {
	if u, err := s.svc.WebURL(r.Context()); err == nil && u != "" {
		return strings.TrimSuffix(u, "/") + "/api/v1"
	}
	scheme := "http"
	if secure(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host + strings.TrimSuffix(basePath(r), "/") + "/api/v1"
}

func (s *Server) v1Spec(asJSON bool) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		doc := s.apiSpec(r)
		w.Header().Set("Access-Control-Allow-Origin", "*") // it is public; online viewers may read it
		if !asJSON {
			w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
			_, _ = w.Write([]byte(doc))
			return
		}
		var v any
		if err := yaml.Unmarshal([]byte(doc), &v); err != nil {
			http.Error(w, "spec: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(v)
	}
}
