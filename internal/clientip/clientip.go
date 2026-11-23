// Package clientip tells which address a request came from, behind our own reverse proxy.
package clientip

import (
	"net"
	"net/http"
	"strings"
)

// From returns the client's address. X-Forwarded-For is believed only on a connection from this
// machine (nginx or Caddy in front of the panel); from anywhere else the header is the client's
// own claim and is ignored. When a local proxy appends to X-Forwarded-For instead of overwriting
// it, the rightmost valid address is the one the local proxy saw on the socket.
func From(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		vals := r.Header.Values("X-Forwarded-For")
		for i := len(vals) - 1; i >= 0; i-- {
			parts := strings.Split(vals[i], ",")
			for j := len(parts) - 1; j >= 0; j-- {
				cand := strings.TrimSpace(parts[j])
				if net.ParseIP(cand) != nil {
					return cand
				}
			}
		}
	}
	return host
}
