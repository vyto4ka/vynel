package clientip

import (
	"net/http"
	"testing"
)

func TestFrom(t *testing.T) {
	for _, c := range []struct{ remote, xff, want string }{
		{"203.0.113.9:5000", "", "203.0.113.9"},
		{"127.0.0.1:5000", "198.51.100.7", "198.51.100.7"},
		{"[::1]:5000", "198.51.100.7, 10.0.0.1", "198.51.100.7"},
		{"127.0.0.1:5000", "", "127.0.0.1"},
		// Not from the local proxy: the header is the client's own claim.
		{"203.0.113.9:5000", "198.51.100.7", "203.0.113.9"},
		{"no-port", "", "no-port"},
	} {
		r := &http.Request{RemoteAddr: c.remote, Header: http.Header{}}
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := From(r); got != c.want {
			t.Errorf("%s + %q: %q, want %q", c.remote, c.xff, got, c.want)
		}
	}
}
