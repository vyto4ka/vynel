// Package app assembles the panel process: store, service, CA, reconciler, node gateway and,
// with --with-node, an in-process node agent.
package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/vyto4ka/vpn/internal/node/agent"
	"github.com/vyto4ka/vpn/internal/panel/ca"
	"github.com/vyto4ka/vpn/internal/panel/gateway"
	"github.com/vyto4ka/vpn/internal/panel/reconciler"
	"github.com/vyto4ka/vpn/internal/panel/service"
	"github.com/vyto4ka/vpn/internal/panel/store"
	"github.com/vyto4ka/vpn/internal/xray"
)

// Config configures the panel process.
type Config struct {
	DataDir       string
	GatewayListen string // e.g. :9443
	GatewayAddr   string // public host:port for join tokens; stored in settings when set
	WithNode      bool
	LocalNode     service.NodeInput
	Xray          xray.Binary
	Version       string
	Log           *slog.Logger
}

// Panel is a running panel (exposed for tests).
type Panel struct {
	Service    *service.Service
	CA         *ca.CA
	Reconciler *reconciler.Reconciler
	Gateway    *gateway.Server
	SNI        string
	LocalNode  *agent.Agent
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
	p := &Panel{Service: svc, CA: authority, Reconciler: rec, SNI: sni,
		Gateway: gateway.New(svc, authority, rec, sni, cfg.Log.With("component", "gateway"))}
	return p, func() { st.Close() }, nil
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
	errs := make(chan error, 3)
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
	run("gateway", func() error { return p.Gateway.Serve(ctx, cfg.GatewayListen) })
	if cfg.WithNode {
		node, err := p.Service.EnsureLocalNode(ctx, cfg.LocalNode)
		if err != nil {
			return err
		}
		a, err := agent.New(agent.Config{
			DataDir: filepath.Join(cfg.DataDir, "node"), Xray: cfg.Xray, Version: cfg.Version,
			Log: cfg.Log.With("component", "local-node"), Dial: LocalDialer(ctx, p.Reconciler, node.ID),
		})
		if err != nil {
			return err
		}
		defer a.Close()
		p.LocalNode = a
		cfg.Log.Info("local node enabled", "node", node.Code)
		run("local node", func() error { return a.Run(ctx) })
	}
	cfg.Log.Info("panel started", "data", cfg.DataDir, "gateway", cfg.GatewayListen)
	select {
	case <-ctx.Done():
	case err = <-errs:
		cancel()
	}
	wg.Wait()
	return err
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
