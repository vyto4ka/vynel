package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestPublicOnlySpecialRanges(t *testing.T) {
	for _, bad := range []string{
		"127.0.0.1", "0.0.0.1", "10.0.0.1", "169.254.169.254",
		"100.100.100.200", "100.64.0.1", "198.18.0.1", "240.0.0.1",
		"::1", "::ffff:127.0.0.1", "::ffff:100.100.100.200",
		"64:ff9b::a9fe:a9fe", "64:ff9b:1::1",
	} {
		if IsPublicHostAddr(bad) {
			t.Errorf("IsPublicHostAddr(%q) = true, want false", bad)
		}
	}
	for _, good := range []string{"1.1.1.1", "8.8.8.8", "2606:4700:4700::1111"} {
		if !IsPublicHostAddr(good) {
			t.Errorf("IsPublicHostAddr(%q) = false, want true", good)
		}
	}
}

func TestSecretSettingsRedactedInAuditLog(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	const secretPw = "SuperSecretBackupPass!99"
	if err := f.s.SetBackupPassword(ctx, ActorCLI, secretPw); err != nil {
		t.Fatal(err)
	}
	if err := f.s.SetSetting(ctx, ActorCLI, SettingBackupPassword, secretPw+"2"); err != nil {
		t.Fatal(err)
	}
	entries, err := f.s.Audit(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Diff, secretPw) {
			t.Fatalf("secret leaked in audit_log diff: %s", e.Diff)
		}
	}
}
