package agent

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"testing"

	nodev1 "github.com/vyto4ka/vynel/internal/proto/vynel/node/v1"
	"github.com/vyto4ka/vynel/internal/xray/xraytest"
)

// A manual node does not init nginx, caddy or the cert bridge, applies only the Xray part of a
// snapshot (the web part is dropped, not even kept in the state) and refuses web-server actions.
func TestManualNodeSkipsWeb(t *testing.T) {
	bin := xraytest.Binary(t)
	nginxBin := os.Getenv("NGINX_BIN")
	if nginxBin == "" {
		nginxBin = "/usr/sbin/nginx"
	}
	if _, err := os.Stat(nginxBin); err != nil {
		t.Skip("needs nginx: its Init is what must not run")
	}
	dir := t.TempDir()
	_ = os.Chmod(dir, 0o755)
	a, err := New(Config{DataDir: dir, Xray: bin, NginxBin: nginxBin, NginxUser: "root", Manual: true,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if a.nginx != nil || a.caddy != nil || a.certs != nil {
		t.Fatal("a manual node must not init nginx, caddy or the cert bridge")
	}
	// The dirs nginx's Init would create stay absent.
	for _, d := range []string{"web/nginx", "web/tls", "web/www"} {
		if _, err := os.Stat(dir + "/" + d); err == nil {
			t.Fatalf("nginx dir %s was created for a manual node", d)
		}
	}

	// A snapshot's web part is ignored, the Xray part applied.
	ctx := context.Background()
	apiPort := xraytest.FreePort(t)
	cfg := map[string]any{
		"inbounds": []any{},
		"outbounds": []any{xraytest.Direct()},
		"api":      map[string]any{"tag": "api", "listen": "127.0.0.1:" + strconv.Itoa(apiPort), "services": []any{"HandlerService", "StatsService"}},
		"stats":    map[string]any{},
	}
	raw, _ := json.Marshal(cfg)
	s := &nodev1.Snapshot{Revision: 1, Hash: "h1", XrayConfig: raw, NginxConfig: "# a rogue panel's nginx config"}
	if err := a.ApplySnapshot(ctx, s); err != nil {
		t.Fatal(err)
	}
	if a.nginxCfg != "" {
		t.Fatal("the snapshot's nginx config must not be applied")
	}
	if a.cur == nil || a.cur.Hash != "h1" {
		t.Fatalf("state not committed: %+v", a.cur)
	}
	// The web part is not even kept in the state.
	if a.cur.Nginx != "" || a.cur.Caddy != nil {
		t.Fatalf("web config saved into the state: nginx %q caddy %v", a.cur.Nginx, a.cur.Caddy)
	}

	// Restarting the web server answers with a readable error instead of touching anything.
	res, _ := a.command(ctx, &nodev1.Command{Action: nodev1.Command_ACTION_RESTART_CADDY})
	if res.Error == "" || !strings.Contains(res.Error, "manual") {
		t.Fatalf("web restart on a manual node must fail readably, got %+v", res)
	}
}
