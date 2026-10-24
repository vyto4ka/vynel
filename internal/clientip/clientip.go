// Package clientip tells which address a request came from, behind our own reverse proxy.
package clientip

import (
	"net"
	"net/http"
	"strings"
)

// From returns the client's address. X-Forwarded-For is believed only on a connection from this
// machine (nginx or Caddy in front of the panel, which set it themselves); from anywhere else
// the header is the client's own claim and is ignored.
func From(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first, _, _ := strings.Cut(xff, ",")
			return strings.TrimSpace(first)
		}
	}
	return host
}
