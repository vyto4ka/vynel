package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The panel does not fetch external subscriptions from its own server's networks: the address
// is typed in by an admin, and a fetch must not become a probe of 127.0.0.1 or the cloud
// metadata service.
func TestExternalSubscriptionRefusesPrivateAddresses(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte("vless://x@example.com:443#a"))
	}))
	defer srv.Close()
	f := newFixture(t)

	FetchPrivate = false
	if _, err := f.s.fetchExternal(context.Background(), srv.URL+"/sub/abc", 1); err == nil || hits != 0 {
		t.Fatalf("fetched a loopback subscription: err %v, hits %d", err, hits)
	}
	// A redirect into the private range is refused as well.
	redir := httptest.NewServer(http.RedirectHandler("http://169.254.169.254/latest/meta-data/", http.StatusFound))
	defer redir.Close()
	if _, err := f.s.fetchExternal(context.Background(), redir.URL+"/sub/abc", 1); err == nil {
		t.Fatal("followed a redirect to the metadata address")
	}

	FetchPrivate = true
	t.Cleanup(func() { FetchPrivate = false })
	if _, err := f.s.fetchExternal(context.Background(), srv.URL+"/sub/abc", 1); err != nil || hits != 1 {
		t.Fatalf("test servers must stay reachable with FetchPrivate: err %v, hits %d", err, hits)
	}
}
