// Package app assembles the panel process: store, service, CA, reconciler, node gateway and,
// with --with-node, an in-process node agent.
package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/vyto4ka/vynel/internal/node/agent"
	"github.com/vyto4ka/vynel/internal/node/caddy"
	"github.com/vyto4ka/vynel/internal/node/firewall"
	"github.com/vyto4ka/vynel/internal/node/nginx"
	"github.com/vyto4ka/vynel/internal/panel/bot"
	"github.com/vyto4ka/vynel/internal/panel/ca"
	"github.com/vyto4ka/vynel/internal/panel/gateway"
	"github.com/vyto4ka/vynel/internal/panel/reconciler"
	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
	"github.com/vyto4ka/vynel/internal/panel/subscription"
	"github.com/vyto4ka/vynel/internal/panel/webapi"
	nodev1 "github.com/vyto4ka/vynel/internal/proto/vynel/node/v1"
	"github.com/vyto4ka/vynel/internal/update"
	"github.com/vyto4ka/vynel/internal/xray"
)

// Config configures the panel process.
type Config struct {
	DataDir       string
	GatewayListen string // e.g. :9443
	GatewayAddr   string // public host:port for join tokens; stored in settings when set
	WithNode      bool
	LocalNode     service.NodeInput
	Xray          xray.Binary
	CaddyBin      string
	NginxBin      string
	WWWDir        string // nginx's public files; "" = under DataDir (tests)
	TuneSysctl    bool
	Firewall      bool // manage nftables (`vynel firewall on|off` decides whether rules are loaded)
	Version       string
	Log           *slog.Logger
}

// Panel is a running panel (exposed for tests).
type Panel struct {
	Service       *service.Service
	CA            *ca.CA
	Reconciler    *reconciler.Reconciler
	Gateway       *gateway.Server
	Subscriptions *subscription.Handler
	Web           *webapi.Server
	Bot           *bot.Bot
	SNI           string
	LocalNode     *agent.Agent
}

// Open prepares everything without starting network listeners.
func Open(ctx context.Context, cfg Config) (*Panel, func(), error) {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, nil, err
	}
	st, err := store.Open(ctx, filepath.Join(cfg.DataDir, "panel.db"))
	if err != nil {
		return nil, nil, err
	}
	svc := service.New(st)
	if err := svc.EnsureDefaults(ctx); err != nil {
		st.Close()
		return nil, nil, err
	}
	if err := svc.EnsureWebDefaults(ctx); err != nil {
		st.Close()
		return nil, nil, err
	}
	authority, err := ca.LoadOrCreate(filepath.Join(cfg.DataDir, "ca"))
	if err != nil {
		st.Close()
		return nil, nil, err
	}
	sni, err := svc.Setting(ctx, service.SettingGatewaySNI, "")
	if err != nil {
		st.Close()
		return nil, nil, err
	}
	if sni == "" {
		b := make([]byte, 3)
		_, _ = rand.Read(b)
		sni = "gw-" + hex.EncodeToString(b) + ".internal"
		if err := svc.SetSetting(ctx, service.ActorSystem, service.SettingGatewaySNI, sni); err != nil {
			st.Close()
			return nil, nil, err
		}
	}
	if cfg.GatewayAddr != "" {
		cur, _ := svc.Setting(ctx, service.SettingGatewayAddr, "")
		if cur != cfg.GatewayAddr {
			if err := svc.SetSetting(ctx, service.ActorSystem, service.SettingGatewayAddr, cfg.GatewayAddr); err != nil {
				st.Close()
				return nil, nil, err
			}
		}
	}
	rec := reconciler.New(svc, cfg.Log.With("component", "reconciler"))
	svc.OnChange = rec.Notify
	svc.NodeConnected = rec.Connected
	p := &Panel{Service: svc, CA: authority, Reconciler: rec, SNI: sni,
		Gateway:       gateway.New(svc, authority, rec, sni, cfg.Log.With("component", "gateway")),
		Subscriptions: subscription.NewHandler(svc, cfg.Log.With("component", "subscriptions"))}
	p.Bot = bot.New(bot.Config{Service: svc, DataDir: cfg.DataDir, Version: cfg.Version, Connected: rec.Connected,
		Log: cfg.Log.With("component", "bot"), API: os.Getenv("VYNEL_TELEGRAM_API")}) // own Bot API server / proxy if api.telegram.org is blocked
	p.Web = webapi.New(webapi.Config{Service: svc, CAFingerprint: authority.Fingerprint, Connected: rec.Connected,
		Version: cfg.Version, Log: cfg.Log.With("component", "web"), Bot: p.Bot, NodeAction: nodeAction(rec),
		UpdateStatus: rec.UpdateStatus, XrayTest: xrayTester(cfg.Xray)})
	return p, func() { st.Close() }, nil
}

// xrayTester lets this server's Xray read a config before the panel hands it to nodes; nil when
// the panel has no Xray of its own (a panel without a local node).
func xrayTester(bin xray.Binary) func(ctx context.Context, cfg []byte) error {
	if bin.Path == "" {
		return nil
	}
	if _, err := os.Stat(bin.Path); err != nil {
		return nil
	}
	return bin.Test
}

// nodeAction runs the panel's node actions through the node's live session.
func nodeAction(rec *reconciler.Reconciler) func(ctx context.Context, nodeID int64, action string) (string, error) {
	commands := map[string]nodev1.Command_Action{
		"xray": nodev1.Command_ACTION_RESTART_XRAY, "caddy": nodev1.Command_ACTION_RESTART_CADDY,
		"service": nodev1.Command_ACTION_RESTART_AGENT, "reboot": nodev1.Command_ACTION_REBOOT, "update": nodev1.Command_ACTION_UPDATE,
	}
	return func(ctx context.Context, nodeID int64, action string) (string, error) {
		if action == "resync" {
			if err := rec.Resync(nodeID); err != nil {
				return "", errors.New("нода не на связи")
			}
			return "конфиг отправляется заново", nil
		}
		cmd, ok := commands[action]
		if !ok {
			return "", fmt.Errorf("unknown action %q", action)
		}
		c := &nodev1.Command{Action: cmd}
		if cmd == nodev1.Command_ACTION_UPDATE {
			// Nodes update to the release the panel follows, so the panel and its nodes run one version.
			src := update.LoadSource()
			c.Release, c.Ref = src.Release, src.Ref
		}
		ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		res, err := rec.Command(ctx, nodeID, c)
		switch {
		case errors.Is(err, reconciler.ErrNotConnected):
			return "", errors.New("нода не на связи")
		case err != nil:
			return "", err
		case res.Error != "":
			return "", errors.New(res.Error)
		}
		return res.Detail, nil
	}
}

// Run runs the panel until ctx ends.
func Run(ctx context.Context, cfg Config) error {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	p, closeFn, err := Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer closeFn()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	errs := make(chan error, 16) // one slot per runner: a failing runner never blocks
	run := func(name string, fn func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := fn(); err != nil && ctx.Err() == nil {
				errs <- fmt.Errorf("%s: %w", name, err)
			}
		}()
	}
	run("reconciler", func() error { return p.Reconciler.Run(ctx) })
	run("webhooks", func() error { return p.Service.RunWebhooks(ctx) })
	run("gateway", func() error { return p.Gateway.Serve(ctx, cfg.GatewayListen) })
	subListen, err := p.Service.Setting(ctx, service.SettingSubListen, service.DefaultSubListen)
	if err != nil {
		return err
	}
	run("subscriptions", func() error { return p.Subscriptions.Serve(ctx, subListen) })
	webListen, err := p.Service.Setting(ctx, service.SettingWebListen, service.DefaultWebListen)
	if err != nil {
		return err
	}
	run("web", func() error { return p.Web.Serve(ctx, webListen) })
	run("bot", func() error { return p.Bot.Run(ctx) })
	var fw *firewall.Manager
	var trustedVPN func() []string // set once the local node exists
	if cfg.Firewall {
		fw = &firewall.Manager{Log: cfg.Log.With("component", "firewall"), Trusted: func() []string {
			ips := p.Service.WebSessionIPs(ctx)
			if trustedVPN != nil {
				ips = append(ips, trustedVPN()...)
			}
			return ips
		}}
	}
	if cfg.WithNode {
		node, err := p.Service.EnsureLocalNode(ctx, cfg.LocalNode)
		if err != nil {
			return err
		}
		a, err := agent.New(agent.Config{
			DataDir: filepath.Join(cfg.DataDir, "node"), Xray: cfg.Xray, Version: cfg.Version,
			Log: cfg.Log.With("component", "local-node"), Dial: LocalDialer(ctx, p.Reconciler, node.ID),
			CaddyBin: cfg.CaddyBin, NginxBin: cfg.NginxBin, WWWDir: cfg.WWWDir, TuneSysctl: cfg.TuneSysctl,
			Firewall: fw, ExtraPorts: gatewayPorts(ctx, p.Service, cfg.GatewayListen),
		})
		if err != nil {
			return err
		}
		defer a.Close()
		p.LocalNode = a
		trustedVPN = a.TrustedIPs
		cfg.Log.Info("local node enabled", "node", node.Code)
		run("local node", func() error { return a.Run(ctx) })
	} else if web := filepath.Join(cfg.DataDir, "web"); (&nginx.Manager{Bin: cfg.NginxBin}).Available() {
		// No local node: the panel runs nginx itself for the subscription and panel domains.
		m := &nginx.Manager{Bin: cfg.NginxBin, DataDir: web, WWWDir: cfg.WWWDir, Log: cfg.Log.With("component", "nginx"),
			CaddyStorage: filepath.Join(web, "caddy", "caddy")}
		if err := m.Init(); err != nil {
			return err
		}
		defer m.Close()
		run("nginx", func() error { return panelNginxLoop(ctx, p.Service, m, cfg.Log) })
		if fw != nil {
			gw := gatewayPorts(ctx, p.Service, cfg.GatewayListen)
			run("firewall", func() error {
				fw.Loop(ctx, 15*time.Second, func() []firewall.Port {
					c, _ := p.Service.PanelNginxConfig(ctx)
					return append(firewall.NginxPorts(c), gw()...)
				})
				return nil
			})
		}
	} else {
		// Without nginx (not installed yet): Caddy, as before.
		m := &caddy.Manager{Bin: cfg.CaddyBin, DataDir: web, Log: cfg.Log.With("component", "caddy")}
		defer m.Close()
		run("caddy", func() error { return panelCaddyLoop(ctx, p.Service, m, cfg.Log) })
		if fw != nil {
			gw := gatewayPorts(ctx, p.Service, cfg.GatewayListen)
			run("firewall", func() error {
				fw.Loop(ctx, 15*time.Second, func() []firewall.Port {
					c, _ := p.Service.PanelCaddyConfig(ctx)
					return append(firewall.CaddyPorts(c), gw()...)
				})
				return nil
			})
		}
	}
	run("web sessions", func() error { p.Service.WebSessionsLoop(ctx, 5*time.Second); return nil })
	cfg.Log.Info("panel started", "data", cfg.DataDir, "gateway", cfg.GatewayListen)
	select {
	case <-ctx.Done():
	case err = <-errs:
		cancel()
	}
	wg.Wait()
	return err
}

// gatewayPorts is the node gateway port for the firewall: open to the nodes' addresses only,
// to everyone while a new node may join (service.GatewayAccess).
func gatewayPorts(ctx context.Context, svc *service.Service, listen string) func() []firewall.Port {
	return func() []firewall.Port {
		ps := firewall.ListenPort(listen, "node gateway")
		if len(ps) == 0 {
			return nil
		}
		acc, err := svc.GatewayAccess(ctx)
		if err != nil || acc.Open {
			return ps
		}
		ps[0].From, ps[0].Restricted = acc.IPs, true
		return ps
	}
}

// LocalDialer connects an in-process agent to the reconciler.
func LocalDialer(ctx context.Context, rec *reconciler.Reconciler, nodeID int64) agent.Dialer {
	return func(dctx context.Context) (agent.Conn, error) {
		panelEnd, nodeEnd := reconciler.Pipe(dctx)
		go func() {
			_ = rec.Serve(nodeID, panelEnd)
			nodeEnd.Close()
		}()
		return nodeEnd, nil
	}
}

// panelNginxLoop keeps the panel's own nginx in line with the subscription and panel settings
// and renews its certificates.
func panelNginxLoop(ctx context.Context, svc *service.Service, m *nginx.Manager, log *slog.Logger) error {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	var lastErr string
	go func() {
		for {
			if err := m.RenewCerts(ctx); err != nil {
				log.Warn("panel certificates", "err", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Minute):
			}
		}
	}()
	for {
		conf, err := svc.PanelNginxConfig(ctx)
		if err == nil {
			err = m.Apply(ctx, conf)
		}
		if err != nil && err.Error() != lastErr {
			log.Error("panel nginx", "err", err)
		}
		lastErr = ""
		if err != nil {
			lastErr = err.Error()
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// panelCaddyLoop keeps the panel's own Caddy in line with the subscription settings.
func panelCaddyLoop(ctx context.Context, svc *service.Service, m *caddy.Manager, log *slog.Logger) error {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	var lastErr string
	for {
		cfg, err := svc.PanelCaddyConfig(ctx)
		if err == nil && len(cfg) > 0 && !m.Available() {
			err = errors.New("caddy is not installed; the subscription domain is not served")
		}
		if err == nil {
			err = m.Apply(ctx, cfg)
		}
		if err != nil && err.Error() != lastErr {
			log.Error("panel caddy", "err", err)
		}
		lastErr = ""
		if err != nil {
			lastErr = err.Error()
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}
