package clientip

import (
	"net/http"
	"testing"
)

func TestFrom(t *testing.T) {
	for _, c := range []struct {
		remote string
		xff    []string
		realIP string
		want   string
	}{
		{"203.0.113.9:5000", nil, "", "203.0.113.9"},
		{"127.0.0.1:5000", []string{"198.51.100.7"}, "", "198.51.100.7"},
		// A spoofed client X-Forwarded-For before the local proxy's hop must not win.
		{"[::1]:5000", []string{"1.2.3.4, 198.51.100.7"}, "", "198.51.100.7"},
		{"127.0.0.1:5000", []string{"1.2.3.4", "198.51.100.7"}, "", "198.51.100.7"},
		// X-Real-IP is not overwritten by Caddy/ALB/Envoy, so it must never override XFF or loopback.
		{"127.0.0.1:5000", []string{"198.51.100.7"}, "8.8.8.8", "198.51.100.7"},
		{"127.0.0.1:5000", nil, "8.8.8.8", "127.0.0.1"},
		// Garbage in X-Forwarded-For is ignored.
		{"127.0.0.1:5000", []string{"not-an-ip"}, "", "127.0.0.1"},
		{"127.0.0.1:5000", nil, "", "127.0.0.1"},
		// Not from the local proxy: the header is the client's own claim.
		{"203.0.113.9:5000", []string{"198.51.100.7"}, "8.8.8.8", "203.0.113.9"},
		{"no-port", nil, "", "no-port"},
	} {
		r := &http.Request{RemoteAddr: c.remote, Header: http.Header{}}
		for _, v := range c.xff {
			r.Header.Add("X-Forwarded-For", v)
		}
		if c.realIP != "" {
			r.Header.Set("X-Real-IP", c.realIP)
		}
		if got := From(r); got != c.want {
			t.Errorf("%s + xff=%v real=%q: %q, want %q", c.remote, c.xff, c.realIP, got, c.want)
		}
	}
}
