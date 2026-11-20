// Package agent is the node side: it keeps a session with the panel, applies snapshots and
// deltas to Xray and survives panel outages on its saved state (docs/ARCHITECTURE.md §4.2).
package agent

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/vyto4ka/vynel/internal/certs"
	"github.com/vyto4ka/vynel/internal/node/caddy"
	"github.com/vyto4ka/vynel/internal/node/firewall"
	"github.com/vyto4ka/vynel/internal/node/metrics"
	"github.com/vyto4ka/vynel/internal/node/nginx"
	"github.com/vyto4ka/vynel/internal/node/state"
	"github.com/vyto4ka/vynel/internal/node/sysctl"
	nodev1 "github.com/vyto4ka/vynel/internal/proto/vynel/node/v1"
	"github.com/vyto4ka/vynel/internal/update"
	"github.com/vyto4ka/vynel/internal/xray"
	"github.com/vyto4ka/vynel/internal/xrayconf"
)

// Conn is the node side of a panel connection (gRPC client stream or in-memory pipe).
type Conn interface {
	Send(*nodev1.NodeMessage) error
	Recv() (*nodev1.PanelMessage, error)
}

// Dialer opens a new connection to the panel.
type Dialer func(ctx context.Context) (Conn, error)

// Config configures an agent.
type Config struct {
	DataDir       string // state.db and xray config live here
	Xray          xray.Binary
	Version       string
	Dial          Dialer
	Log           *slog.Logger
	Addresses     func() []*nodev1.Address // defaults to the host's interfaces
	MaxBackoff    time.Duration            // reconnect backoff cap, default 30s
	StatsInterval time.Duration            // how often counters are collected, default 10s
	// OnlineInterval is how often the addresses of online users are sent, default 5s (only
	// changes; the same list again every 30s).
	OnlineInterval time.Duration
	CaddyBin       string        // path to caddy ("" = not installed); only for panels that still send Caddy configs
	NginxBin       string        // path to nginx ("" = /usr/sbin/nginx)
	WWWDir         string        // decoys, ACME webroot and temp files nginx workers use; "" = <DataDir>/web/www
	NginxUser      string        // nginx worker user; "" = www-data (tests use root)
	TuneSysctl     bool          // apply BBR/fq/TFO on start (needs root)
	Manual         bool          // manual node: no nginx/certs (the admin runs the web server); only Xray is managed
	CertInterval   time.Duration // how often certificates are renewed and copied for Xray, default 1m
	// Firewall keeps nftables in step with what this node serves (when `vynel firewall on`);
	// nil = never touch the firewall (tests).
	Firewall   *firewall.Manager
	ExtraPorts func() []firewall.Port // also let in (the panel's node gateway on an all-in-one server)
	// Restart restarts the whole service (admin action). Default: SIGTERM to this process, and
	// systemd (Restart=always) starts it again.
	Restart func()
	// Reboot reboots the server (admin action). Default: systemctl reboot.
	Reboot func() error
	// Update starts updating vynel, Xray and nginx in the background (admin action), first
	// switching to the release the panel follows ("" = the node's own). Default:
	// update.StartFollowing.
	Update func(ctx context.Context, release, ref string) error
	// UpdateStatus reports how the last update went. Default: update.LocalStatus.
	UpdateStatus func() *update.Status
	// UpdateEvery is how often a running update is reported, default 1s.
	UpdateEvery time.Duration
	// FactsInterval is how often sysctl/NTP warnings and nginx version are re-checked and sent
	// to the panel when they change, default 15s.
	FactsInterval time.Duration
	// CheckFacts overrides sysctl.Check in tests.
	CheckFacts func() []string
}

// Agent applies the panel's desired state to Xray.
type Agent struct {
	cfg   Config
	log   *slog.Logger
	st    *state.Store
	proc  *xray.Process
	caddy *caddy.Manager
	nginx *nginx.Manager

	mu           sync.Mutex
	cur          *state.State
	api          *xray.API
	apiAddr      string
	xrayVer      string
	caddyVer     string
	nginxVer     string
	warnings     []string
	metrics      metrics.Collector
	statsCh      chan struct{} // a batch was queued
	certKick     chan struct{} // a new web config may need certificates: issue now, not at the next tick
	certReportCh chan struct{} // certificate statuses changed: push a CertStatusReport to the panel
	certs        *certBridge
	certDoms     []string // domains whose certificates the running Xray config uses
	caddyCfg     []byte   // the config Caddy runs now (it may be ahead of a.cur when Xray fails)
	nginxCfg     string   // the config nginx runs now; only one of the two web servers runs
	vpnIPs       []string // addresses of connected VPN users, trusted by the firewall
	// online is the last reading of who is connected from where (onlineLoop): the panel's
	// limit of addresses and the stats batches both use it, so Xray is asked once.
	online   *nodev1.OnlineIPs
	onlineAt time.Time
	// ipBlocks are the panel's addresses over users' limits; blocksApplied: the running Xray
	// has block rules in front of its own.
	ipBlocks      []*nodev1.IPBlock
	blocksApplied bool
}

// New opens the agent's state. Call Run to start.
func New(cfg Config) (*Agent, error) {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.Addresses == nil {
		cfg.Addresses = LocalAddresses
	}
	if cfg.MaxBackoff == 0 {
		cfg.MaxBackoff = 30 * time.Second
	}
	if cfg.StatsInterval == 0 {
		cfg.StatsInterval = 10 * time.Second
	}
	if cfg.OnlineInterval == 0 {
		cfg.OnlineInterval = 5 * time.Second
	}
	if cfg.CertInterval == 0 {
		cfg.CertInterval = time.Minute
	}
	if cfg.Restart == nil {
		cfg.Restart = selfTerm
	}
	if cfg.Update == nil {
		cfg.Update = update.StartFollowing
	}
	if cfg.UpdateStatus == nil {
		cfg.UpdateStatus = update.LocalStatus
	}
	if cfg.UpdateEvery == 0 {
		cfg.UpdateEvery = time.Second
	}
	if cfg.FactsInterval == 0 {
		cfg.FactsInterval = 15 * time.Second
	}
	if cfg.CheckFacts == nil {
		cfg.CheckFacts = sysctl.Check
	}
	if cfg.Reboot == nil {
		cfg.Reboot = func() error { return exec.Command("systemctl", "reboot").Run() }
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, err
	}
	st, err := state.Open(filepath.Join(cfg.DataDir, "state.db"))
	if err != nil {
		return nil, err
	}
	web := filepath.Join(cfg.DataDir, "web")
	www := cfg.WWWDir
	if www == "" {
		www = filepath.Join(web, "www")
	}
	a := &Agent{cfg: cfg, log: cfg.Log, st: st, proc: xray.NewProcess(cfg.Xray, filepath.Join(cfg.DataDir, "xray.json"), cfg.Log),
		caddy: &caddy.Manager{Bin: cfg.CaddyBin, DataDir: web, Log: cfg.Log.With("component", "caddy")},
		nginx: &nginx.Manager{Bin: cfg.NginxBin, DataDir: web, WWWDir: www, User: cfg.NginxUser, Log: cfg.Log.With("component", "nginx"),
			CaddyStorage: filepath.Join(web, "caddy", "caddy")},
		statsCh: make(chan struct{}, 1), certKick: make(chan struct{}, 1), certReportCh: make(chan struct{}, 1)}
	if !cfg.Manual {
		if err := a.nginx.Init(); err != nil {
			st.Close()
			return nil, err
		}
		a.certs = newCertBridge(cfg.DataDir, a.nginx.Certs.Dir, a.nginx.DecoyDir())
		a.nginx.OnCerts = func() {
			a.mu.Lock()
			a.syncCertsLocked(context.Background())
			a.mu.Unlock()
			a.notifyCerts()
		}
	} else {
		// A manual node runs no web server of its own: nginx and certificates are the admin's.
		a.caddy, a.nginx = nil, nil
	}
	if a.cur, err = st.Load(); err != nil {
		st.Close()
		return nil, err
	}
	return a, nil
}

// Close stops Xray and closes the state.
func (a *Agent) Close() {
	a.proc.Close()
	if a.caddy != nil {
		a.caddy.Close()
	}
	if a.nginx != nil {
		a.nginx.Close()
	}
	a.mu.Lock()
	if a.api != nil {
		a.api.Close()
	}
	a.mu.Unlock()
	a.st.Close()
}

// AppliedHash returns the hash of the applied state ("" if none).
func (a *Agent) AppliedHash() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cur == nil {
		return ""
	}
	return a.cur.Hash
}

// refreshFacts re-reads sysctl/NTP warnings and nginx version, returning true when either changed.
func (a *Agent) refreshFacts(ctx context.Context) bool {
	warnings := slices.Clone(a.cfg.CheckFacts())
	var nginxVer string
	if a.nginx != nil {
		nginxVer = a.nginx.Version(ctx)
		if nginxVer == "" {
			warnings = append(warnings, "nginx is not installed: apt install nginx libnginx-mod-stream")
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if slices.Equal(a.warnings, warnings) && a.nginxVer == nginxVer {
		return false
	}
	a.warnings = warnings
	a.nginxVer = nginxVer
	return true
}

func (a *Agent) buildHello() *nodev1.Hello {
	addrs := a.cfg.Addresses()
	a.mu.Lock()
	applied := ""
	if a.cur != nil {
		applied = a.cur.Hash
	}
	warnings := slices.Clone(a.warnings)
	nginxVer := a.nginxVer
	a.mu.Unlock()
	hello := &nodev1.Hello{
		AgentVersion: a.cfg.Version, XrayVersion: a.xrayVer, AppliedHash: applied,
		Os: runtime.GOOS, Arch: runtime.GOARCH, Addresses: addrs,
		CaddyVersion: a.caddyVer, Warnings: warnings, NginxVersion: nginxVer,
	}
	hello.Capabilities = append(hello.Capabilities, CapChunks, CapIPLimit)
	if !a.cfg.Manual && nginxVer != "" {
		hello.Capabilities = append(hello.Capabilities, CapNginx, CapCerts)
	}
	return hello
}

// CapCerts is the capability of agents that report certificate statuses and accept ACTION_RENEW_CERTS.
const CapCerts = "certs"

func (a *Agent) notifyCerts() {
	select {
	case a.certReportCh <- struct{}{}:
	default:
	}
}

// CertStatus reads the status of a single domain certificate from this node's nginx manager.
func (a *Agent) CertStatus(domain string) (certs.Status, bool) {
	if a == nil || a.nginx == nil || a.nginx.Certs == nil {
		return certs.Status{}, false
	}
	st, err := a.nginx.Certs.Status(domain)
	return st, err == nil
}

// CertStatuses returns the statuses of all certificate domains managed by this node.
func (a *Agent) CertStatuses() []certs.Status {
	if a == nil || a.nginx == nil {
		return nil
	}
	return a.nginx.CertStatuses()
}

// ForceRenewCerts force-renews certificates for domain (or all configured domains when empty),
// reloads nginx gracefully, syncs Xray certificates and notifies the panel.
func (a *Agent) ForceRenewCerts(ctx context.Context, domain string) ([]string, error) {
	if a == nil || a.cfg.Manual || a.nginx == nil {
		return nil, errors.New("a manual node runs no managed web server")
	}
	done, err := a.nginx.ForceRenewCerts(ctx, domain)
	a.mu.Lock()
	if a.certs != nil {
		a.syncCertsLocked(ctx)
	}
	a.mu.Unlock()
	a.notifyCerts()
	return done, err
}

func (a *Agent) certReport() *nodev1.CertStatusReport {
	sts := a.CertStatuses()
	if len(sts) == 0 {
		return nil
	}
	rep := &nodev1.CertStatusReport{Certs: make([]*nodev1.CertInfo, 0, len(sts))}
	for _, st := range sts {
		ci := &nodev1.CertInfo{
			Domain:      st.Domain,
			Issuer:      st.Issuer,
			Placeholder: st.Placeholder,
			Ok:          !st.NotAfter.IsZero(),
			LastError:   st.LastError,
		}
		if !st.NotAfter.IsZero() {
			ci.NotAfter = st.NotAfter.Unix()
		}
		if !st.RetryAt.IsZero() {
			ci.RetryAt = st.RetryAt.Unix()
		}
		rep.Certs = append(rep.Certs, ci)
	}
	return rep
}

// Run starts Xray from the saved state and keeps a session with the panel until ctx ends.
func (a *Agent) Run(ctx context.Context) error {
	if v, err := a.cfg.Xray.Version(ctx); err == nil {
		a.xrayVer = v
	} else {
		a.log.Warn("cannot get xray version", "err", err)
	}
	if a.cfg.TuneSysctl {
		_ = sysctl.Tune(true)
	}
	if a.caddy != nil {
		a.caddyVer = a.caddy.Version(ctx)
	}
	a.refreshFacts(ctx)
	a.mu.Lock()
	if a.cur != nil {
		if err := a.applyWebLocked(ctx, a.cur.Caddy, a.cur.Nginx); err != nil {
			a.log.Error("cannot start the web server from saved state", "err", err)
		}
		if err := a.restartLocked(ctx, a.cur); err != nil {
			a.log.Error("cannot start xray from saved state", "err", err)
		} else {
			a.log.Info("xray started from saved state", "revision", a.cur.Revision)
		}
	}
	a.mu.Unlock()
	go a.collectLoop(ctx)
	go a.onlineLoop(ctx)
	if a.certs != nil {
		go a.certLoop(ctx)
	}
	if a.cfg.Firewall != nil {
		go a.cfg.Firewall.Loop(ctx, 15*time.Second, a.FirewallPorts)
	}

	backoff := min(time.Second, a.cfg.MaxBackoff)
	for ctx.Err() == nil {
		started := time.Now()
		err := a.session(ctx)
		if ctx.Err() != nil {
			break
		}
		if time.Since(started) > time.Minute {
			backoff = min(time.Second, a.cfg.MaxBackoff)
		}
		if st := a.cfg.UpdateStatus(); st != nil && (st.Running || time.Since(time.Unix(st.FinishedAt, 0)) < 2*time.Minute) {
			// Reconnect quickly during and right after an update so live and final logs reach a restarted panel immediately.
			backoff = min(backoff, min(2*time.Second, a.cfg.MaxBackoff))
		}
		if status.Code(err) == codes.PermissionDenied {
			// Revoked or deleted on the panel: retrying fast is pointless.
			backoff = a.cfg.MaxBackoff
			a.log.Error("the panel rejected this node; get a new token and run `vynel node join TOKEN`", "err", err)
		} else {
			a.log.Warn("panel session ended, reconnecting", "err", err, "in", backoff)
		}
		select {
		case <-ctx.Done():
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, a.cfg.MaxBackoff)
	}
	return ctx.Err()
}

func (a *Agent) session(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	conn, err := a.cfg.Dial(ctx)
	if err != nil {
		return err
	}
	a.refreshFacts(ctx)
	hello := a.buildHello()
	if err := conn.Send(&nodev1.NodeMessage{Msg: &nodev1.NodeMessage_Hello{Hello: hello}}); err != nil {
		return err
	}
	a.log.Info("connected to panel", "applied", short(hello.AppliedHash))

	msgs := make(chan *nodev1.PanelMessage)
	errs := make(chan error, 1)
	go func() {
		for {
			m, err := conn.Recv()
			if err != nil {
				errs <- err
				return
			}
			select {
			case msgs <- m:
			case <-ctx.Done():
				return
			}
		}
	}()
	addrTick := time.NewTicker(5 * time.Minute)
	defer addrTick.Stop()
	factsTick := time.NewTicker(a.cfg.FactsInterval)
	defer factsTick.Stop()
	resendTick := time.NewTicker(time.Minute)
	defer resendTick.Stop()
	var sentUpTo uint64       // highest seq sent in this session
	parts := &snapshotParts{} // a snapshot sent in parts, while they come in
	// Send the update status right after Hello (before flushing queued stats batches) so a
	// version change in Hello never races ahead of the update's log on the panel.
	var lastUpd *nodev1.UpdateStatus
	var watchUntil time.Time // right after an update command its "running" record may not be there yet
	watching := false
	sendUpdate := func() error {
		st := a.cfg.UpdateStatus()
		watching = (st != nil && st.Running) || time.Now().Before(watchUntil)
		if st == nil {
			return nil
		}
		msg := &nodev1.UpdateStatus{Running: st.Running, Ok: st.OK, StartedAt: st.StartedAt, FinishedAt: st.FinishedAt, Version: st.Version, Log: st.Log}
		if lastUpd != nil && proto.Equal(lastUpd, msg) {
			return nil
		}
		lastUpd = msg
		return conn.Send(&nodev1.NodeMessage{Msg: &nodev1.NodeMessage_Update{Update: msg}})
	}
	if err := sendUpdate(); err != nil {
		return err
	}
	sendStats := func() error {
		batches, err := a.st.Pending(sentUpTo, 50)
		if err != nil {
			return err
		}
		for _, b := range batches {
			if err := conn.Send(&nodev1.NodeMessage{Msg: &nodev1.NodeMessage_Stats{Stats: b}}); err != nil {
				return err
			}
			sentUpTo = b.Seq
		}
		return nil
	}
	if err := sendStats(); err != nil {
		return err
	}
	var lastCerts *nodev1.CertStatusReport
	sendCerts := func() error {
		rep := a.certReport()
		if rep == nil {
			return nil
		}
		if lastCerts != nil && proto.Equal(lastCerts, rep) {
			return nil
		}
		lastCerts = rep
		return conn.Send(&nodev1.NodeMessage{Msg: &nodev1.NodeMessage_Certs{Certs: rep}})
	}
	if err := sendCerts(); err != nil {
		return err
	}
	updTick := time.NewTicker(a.cfg.UpdateEvery)
	defer updTick.Stop()
	onlineTick := time.NewTicker(a.cfg.OnlineInterval)
	defer onlineTick.Stop()
	var lastOnline *nodev1.OnlineIPs
	var lastOnlineAt time.Time
	updTicks := 0
	for {
		select {
		case <-ctx.Done():
			if watching {
				_ = sendUpdate()
			}
			return ctx.Err()
		case <-factsTick.C:
			if a.refreshFacts(ctx) {
				if err := conn.Send(&nodev1.NodeMessage{Msg: &nodev1.NodeMessage_Hello{Hello: a.buildHello()}}); err != nil {
					return err
				}
			}
		case <-onlineTick.C:
			a.mu.Lock()
			msg := a.online
			a.mu.Unlock()
			if msg == nil || (lastOnline != nil && proto.Equal(msg, lastOnline) && time.Since(lastOnlineAt) < 30*time.Second) {
				continue
			}
			if err := conn.Send(&nodev1.NodeMessage{Msg: &nodev1.NodeMessage_OnlineIps{OnlineIps: msg}}); err != nil {
				return err
			}
			lastOnline, lastOnlineAt = msg, time.Now()
		case <-updTick.C:
			updTicks++
			if watching || updTicks%5 == 0 {
				if err := sendUpdate(); err != nil {
					return err
				}
			}
		case <-a.certReportCh:
			if err := sendCerts(); err != nil {
				return err
			}
		case err := <-errs:
			return err
		case <-a.statsCh:
			if err := sendStats(); err != nil {
				return err
			}
		case <-resendTick.C:
			// Anything still queued after a minute was lost or rejected: send it again.
			sentUpTo = 0
			if err := sendStats(); err != nil {
				return err
			}
		case <-addrTick.C:
			msg := &nodev1.NodeMessage{Msg: &nodev1.NodeMessage_Addresses{Addresses: &nodev1.Addresses{Addresses: a.cfg.Addresses()}}}
			if err := conn.Send(msg); err != nil {
				return err
			}
		case m := <-msgs:
			if c, ok := m.Msg.(*nodev1.PanelMessage_Command); ok {
				res, after := a.command(ctx, c.Command)
				if err := conn.Send(&nodev1.NodeMessage{Msg: &nodev1.NodeMessage_Result{Result: res}}); err != nil {
					return err
				}
				if c.Command.Action == nodev1.Command_ACTION_UPDATE && res.Error == "" {
					watching, watchUntil = true, time.Now().Add(30*time.Second)
					if err := sendUpdate(); err != nil {
						return err
					}
				}
				if c.Command.Action == nodev1.Command_ACTION_RENEW_CERTS {
					if err := sendCerts(); err != nil {
						return err
					}
				}
				if after != nil {
					// The answer reaches the panel first; then the service goes down.
					time.AfterFunc(time.Second, after)
				}
				continue
			}
			if b, ok := m.Msg.(*nodev1.PanelMessage_IpBlocks); ok {
				a.SetIPBlocks(ctx, b.IpBlocks.Blocks)
				continue
			}
			if sa, ok := m.Msg.(*nodev1.PanelMessage_StatsAck); ok {
				if err := a.st.AckStats(sa.StatsAck.Epoch, sa.StatsAck.Seq); err != nil {
					return err
				}
				if err := sendStats(); err != nil {
					return err
				}
				continue
			}
			ack := a.handle(ctx, m, parts)
			if ack == nil {
				continue
			}
			if err := conn.Send(&nodev1.NodeMessage{Msg: &nodev1.NodeMessage_Ack{Ack: ack}}); err != nil {
				return err
			}
		}
	}
}

func (a *Agent) handle(ctx context.Context, m *nodev1.PanelMessage, parts *snapshotParts) *nodev1.Ack {
	switch msg := m.Msg.(type) {
	case *nodev1.PanelMessage_Snapshot:
		s, err := parts.add(msg.Snapshot)
		if err != nil {
			a.log.Warn("snapshot part dropped", "err", err)
			return &nodev1.Ack{Revision: msg.Snapshot.Revision, Hash: msg.Snapshot.Hash, Error: err.Error()}
		}
		if s == nil {
			return nil // more parts to come; the snapshot is acknowledged as a whole
		}
		ack := &nodev1.Ack{Revision: s.Revision, Hash: s.Hash}
		if err := a.ApplySnapshot(ctx, s); err != nil {
			a.log.Error("apply snapshot failed", "revision", s.Revision, "err", err)
			ack.Error = err.Error()
		} else {
			a.log.Info("snapshot applied", "revision", s.Revision, "inbounds", len(s.Inbounds))
		}
		return ack
	case *nodev1.PanelMessage_Delta:
		d := msg.Delta
		ack := &nodev1.Ack{Revision: d.Revision, Hash: d.Hash}
		if err := a.ApplyDelta(ctx, d); err != nil {
			a.log.Error("apply delta failed", "revision", d.Revision, "err", err)
			ack.Error = err.Error()
		} else {
			a.log.Info("delta applied", "revision", d.Revision, "ops", len(d.Ops))
		}
		return ack
	}
	return nil
}

// command runs an admin action. Restarting the service or rebooting answers first and acts in
// after, once the answer is sent.
func (a *Agent) command(ctx context.Context, c *nodev1.Command) (res *nodev1.CommandResult, after func()) {
	res = &nodev1.CommandResult{Id: c.Id}
	var err error
	switch c.Action {
	case nodev1.Command_ACTION_RESTART_XRAY:
		a.mu.Lock()
		if a.cur == nil {
			err = errors.New("no config applied yet")
		} else {
			err = a.restartLocked(ctx, a.cur)
		}
		a.mu.Unlock()
	case nodev1.Command_ACTION_RESTART_CADDY: // the web server: nginx, or Caddy for an old panel
		switch {
		case a.cfg.Manual:
			err = errors.New("a manual node runs no managed web server")
		case a.nginx.Running():
			err = a.nginx.Restart()
		default:
			err = a.caddy.Restart()
		}
	case nodev1.Command_ACTION_RESTART_AGENT:
		res.Detail = "the service restarts now"
		after = a.cfg.Restart
	case nodev1.Command_ACTION_UPDATE:
		if err = a.cfg.Update(ctx, c.Release, c.Ref); err == nil {
			res.Detail = "the update is running"
		}
	case nodev1.Command_ACTION_RENEW_CERTS:
		var done []string
		done, err = a.ForceRenewCerts(ctx, c.Domain)
		if len(done) > 0 {
			res.Detail = strings.Join(done, ", ")
		}
	case nodev1.Command_ACTION_REBOOT:
		res.Detail = "the server reboots now"
		after = func() {
			if err := a.cfg.Reboot(); err != nil {
				a.log.Error("reboot failed", "err", err)
			}
		}
	default:
		err = fmt.Errorf("unknown action %v", c.Action)
	}
	a.log.Info("admin action", "action", c.Action.String(), "err", err)
	if err != nil {
		res.Error = err.Error()
	}
	return res, after
}

// ApplySnapshot makes the node run the snapshot. Same structure: users are changed through the
// Xray API without a restart. Different structure (or API failure): full config and restart.
func (a *Agent) ApplySnapshot(ctx context.Context, s *nodev1.Snapshot) error {
	next := &state.State{Revision: s.Revision, Hash: s.Hash, Config: s.XrayConfig, Caddy: s.CaddyConfig, Nginx: s.NginxConfig}
	for _, in := range s.Inbounds {
		si := state.Inbound{Tag: in.Tag, Protocol: in.Protocol, Flow: in.Flow}
		for _, u := range in.Users {
			si.Users = append(si.Users, state.User{Email: u.Email, ID: u.Id})
		}
		next.Inbounds = append(next.Inbounds, si)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cfg.Manual {
		// A manual node applies only the Xray part; the panel sends no web config for it, and
		// anything that slips through (a mixed-version panel) is ignored rather than applied.
		next.Caddy, next.Nginx = nil, ""
	}
	var webErr error
	commit := func() error {
		defer a.syncFirewall(ctx)
		if webErr != nil {
			partial := next.Clone()
			partial.Hash = ""
			if err := a.commitLocked(partial); err != nil {
				return err
			}
			return webErr
		}
		return a.commitLocked(next)
	}
	// Handover: when the web server is taking over a TCP port that Xray currently holds (for
	// example adding a second Reality profile on an all-in-one node moves :443 from Xray to
	// nginx's SNI router), restart Xray first so it releases the port before nginx tries to bind
	// it. Otherwise nginx's reload fails with EADDRINUSE while Xray moves to loopback, leaving
	// :443 with no listener at all.
	if a.cur != nil && a.proc.Running() && webTakesXrayPort(a.cur.Config, next.Config, next.Caddy, next.Nginx) {
		if err := a.restartLocked(ctx, next); err != nil {
			return err
		}
		webErr = a.applyWebLocked(ctx, next.Caddy, next.Nginx)
		return commit()
	}
	// Normally the web server goes first: the Reality target must exist before clients arrive.
	// A failure does not stop the VPN config from applying, but the state is saved as "not fully
	// applied" so the panel keeps retrying and shows the error.
	webErr = a.applyWebLocked(ctx, next.Caddy, next.Nginx)
	if webErr == nil {
		a.syncFirewall(ctx) // e.g. port 80 for certificates, even if Xray then fails
	}
	if a.cur != nil && bytes.Equal(a.cur.Config, next.Config) && a.proc.Running() && a.api != nil {
		err := a.syncUsersLocked(ctx, a.cur, next)
		if err == nil {
			return commit()
		}
		a.log.Warn("hot user sync failed, restarting xray with the full config", "err", err)
	}
	if err := a.restartLocked(ctx, next); err != nil {
		return errors.Join(err, webErr)
	}
	if webErr != nil {
		// Xray just restarted and may have released a port the web server needed.
		webErr = a.applyWebLocked(ctx, next.Caddy, next.Nginx)
	}
	return commit()
}

// webTakesXrayPort reports whether the next web config binds a public TCP port that the current
// Xray config holds and the next Xray config gives up.
func webTakesXrayPort(curXray, nextXray, nextCaddy []byte, nextNginx string) bool {
	if bytes.Equal(curXray, nextXray) {
		return false
	}
	nextXrayTCP := map[int]bool{}
	for _, p := range firewall.XrayPorts(nextXray) {
		if p.Proto == "tcp" {
			nextXrayTCP[p.Port] = true
		}
	}
	freed := map[int]bool{}
	for _, p := range firewall.XrayPorts(curXray) {
		if p.Proto == "tcp" && !nextXrayTCP[p.Port] {
			freed[p.Port] = true
		}
	}
	if len(freed) == 0 {
		return false
	}
	for _, p := range firewall.NginxPorts(nextNginx) {
		if p.Proto == "tcp" && freed[p.Port] {
			return true
		}
	}
	for _, p := range firewall.CaddyPorts(nextCaddy) {
		if p.Proto == "tcp" && freed[p.Port] {
			return true
		}
	}
	return false
}

// FirewallPorts is what this node serves: public Xray inbounds, nginx (or Caddy) sites and the extras.
func (a *Agent) FirewallPorts() []firewall.Port {
	a.mu.Lock()
	defer a.mu.Unlock()
	var ports []firewall.Port
	if a.cfg.ExtraPorts != nil {
		ports = append(ports, a.cfg.ExtraPorts()...)
	}
	if a.cur != nil {
		ports = append(ports, firewall.XrayPorts(a.cur.Config)...)
	}
	ports = append(ports, firewall.CaddyPorts(a.caddyCfg)...)
	return append(ports, firewall.NginxPorts(a.nginxCfg)...)
}

// CapNginx is the capability of agents that run Snapshot.nginx_config.
const CapNginx = "nginx"

// applyWebLocked runs the web server a snapshot asks for: nginx when the panel sent an nginx
// config, Caddy when an older panel sent a Caddy one. Only one runs (both want :80 and :443).
// When the new one fails to start, the old one comes back: Reality needs its target.
func (a *Agent) applyWebLocked(ctx context.Context, caddyCfg []byte, nginxCfg string) error {
	if a.cfg.Manual {
		// A manual node's nginx is the admin's: the snapshot's web part is ignored, and the
		// running web config (none of ours) is left alone.
		return nil
	}
	if nginxCfg != "" {
		if a.caddyCfg != nil {
			_ = a.caddy.Apply(ctx, nil)
		}
		if err := a.nginx.Apply(ctx, nginxCfg); err != nil {
			if a.caddyCfg != nil {
				if rerr := a.caddy.Apply(ctx, a.caddyCfg); rerr != nil {
					a.log.Error("cannot bring caddy back", "err", rerr)
					a.caddyCfg = nil
				}
			}
			return fmt.Errorf("nginx: %w", err)
		}
		a.caddyCfg, a.nginxCfg = nil, nginxCfg
		a.notifyCerts()
		// A new site waits for its certificate: ask for it now (the periodic check is a minute).
		select {
		case a.certKick <- struct{}{}:
		default:
		}
		return nil
	}
	wasNginx := a.nginxCfg
	if wasNginx != "" {
		_ = a.nginx.Apply(ctx, "")
		a.nginxCfg = ""
	}
	if err := a.caddy.Apply(ctx, caddyCfg); err != nil {
		a.caddyCfg = nil
		if wasNginx != "" {
			if rerr := a.nginx.Apply(ctx, wasNginx); rerr == nil {
				a.nginxCfg = wasNginx
			}
		}
		return fmt.Errorf("caddy: %w", err)
	}
	a.caddyCfg = caddyCfg
	return nil
}

// TrustedIPs are the addresses of connected VPN users: they proved who they are with their key,
// so the scanner trap never bans them (docs/INSTALL_GUIDE.md §10).
func (a *Agent) TrustedIPs() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.vpnIPs...)
}

// syncFirewall opens new ports right after a config change instead of at the next tick.
func (a *Agent) syncFirewall(ctx context.Context) {
	if a.cfg.Firewall != nil {
		go func() { _ = a.cfg.Firewall.Sync(context.WithoutCancel(ctx), a.FirewallPorts()) }()
	}
}

// ApplyDelta applies user operations on top of the current state.
func (a *Agent) ApplyDelta(ctx context.Context, d *nodev1.Delta) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cur == nil || a.cur.Hash != d.BaseHash {
		return errors.New("delta base mismatch, need a snapshot")
	}
	if a.api == nil || !a.proc.Running() {
		return errors.New("xray is not running")
	}
	next := a.cur.Clone()
	// Removals first, whatever order the panel sent: Xray removes a VLESS user by email but
	// forgets its UUID, so a user renamed to a new email with the same UUID would stop working
	// if the old email were removed after the new one was added.
	ops := slices.Clone(d.Ops)
	slices.SortStableFunc(ops, func(x, y *nodev1.UserOp) int {
		return cmp.Compare(btoi(x.Kind != nodev1.UserOp_KIND_REMOVE), btoi(y.Kind != nodev1.UserOp_KIND_REMOVE))
	})
	for _, op := range ops {
		in := next.Inbound(op.Tag)
		if in == nil {
			return fmt.Errorf("unknown inbound %s", op.Tag)
		}
		switch op.Kind {
		case nodev1.UserOp_KIND_UPSERT:
			u := state.User{Email: op.User.Email, ID: op.User.Id}
			if i := findUser(in.Users, u.Email); i >= 0 {
				if err := a.api.RemoveUser(ctx, in.Tag, u.Email); err != nil {
					return err
				}
				in.Users = append(in.Users[:i], in.Users[i+1:]...)
			}
			if err := a.addUser(ctx, in, u); err != nil {
				return err
			}
			in.Users = append(in.Users, u)
		case nodev1.UserOp_KIND_REMOVE:
			if err := a.api.RemoveUser(ctx, in.Tag, op.User.Email); err != nil {
				return err
			}
			if i := findUser(in.Users, op.User.Email); i >= 0 {
				in.Users = append(in.Users[:i], in.Users[i+1:]...)
			}
		default:
			return fmt.Errorf("unknown op %v", op.Kind)
		}
	}
	next.Revision, next.Hash = d.Revision, d.Hash
	return a.commitLocked(next)
}

// syncUsersLocked turns the users of cur into those of next. Everything that goes is removed
// before anything is added (see ApplyDelta).
func (a *Agent) syncUsersLocked(ctx context.Context, cur, next *state.State) error {
	for _, in := range next.Inbounds {
		old := map[string]string{}
		if ci := cur.Inbound(in.Tag); ci != nil {
			for _, u := range ci.Users {
				old[u.Email] = u.ID
			}
		}
		want := map[string]string{}
		for _, u := range in.Users {
			want[u.Email] = u.ID
		}
		for email, id := range old {
			if wid, ok := want[email]; !ok || wid != id {
				if err := a.api.RemoveUser(ctx, in.Tag, email); err != nil {
					return err
				}
			}
		}
		for _, u := range in.Users {
			if id, ok := old[u.Email]; ok && id == u.ID {
				continue
			}
			if err := a.addUser(ctx, &in, u); err != nil {
				return err
			}
		}
	}
	return nil
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (a *Agent) addUser(ctx context.Context, in *state.Inbound, u state.User) error {
	switch in.Protocol {
	case "vless":
		return a.api.AddVLESSUser(ctx, in.Tag, u.Email, u.ID, in.Flow)
	case "hysteria":
		return a.api.AddHysteriaUser(ctx, in.Tag, u.Email, u.ID)
	}
	return fmt.Errorf("protocol %s is not supported for hot user changes", in.Protocol)
}

// restartLocked writes the full config (structure + clients) and restarts Xray.
func (a *Agent) restartLocked(ctx context.Context, st *state.State) error {
	full, apiAddr, err := FullConfig(st)
	if err != nil {
		return err
	}
	if a.certs != nil {
		resolved, doms, rerr := a.certs.resolve(full)
		if rerr != nil {
			return rerr
		}
		full, a.certDoms = resolved, doms
	}
	if a.api != nil && a.proc.Running() {
		// Xray counters live in memory: collect them before the restart drops them.
		a.collectLocked(ctx)
	}
	if err := a.proc.Apply(ctx, full); err != nil {
		return err
	}
	if a.api == nil || a.apiAddr != apiAddr {
		if a.api != nil {
			a.api.Close()
		}
		if a.api, err = xray.DialAPI(apiAddr); err != nil {
			return err
		}
		a.apiAddr = apiAddr
	}
	wctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := a.api.WaitReady(wctx); err != nil {
		return fmt.Errorf("%w; xray output: %v", err, a.proc.Tail())
	}
	// A new Xray runs the config's rules only: the blocks go in front again.
	a.blocksApplied = false
	if err := a.applyBlocksLocked(ctx, st); err != nil {
		a.log.Error("cannot apply address blocks after the restart", "err", err)
	}
	return nil
}

func (a *Agent) commitLocked(next *state.State) error {
	if err := a.st.Save(next); err != nil {
		return err
	}
	a.cur = next
	return nil
}

// FullConfig injects clients into the structural config and returns it with the API address.
func FullConfig(st *state.State) ([]byte, string, error) {
	var cfg map[string]any
	if err := json.Unmarshal(st.Config, &cfg); err != nil {
		return nil, "", fmt.Errorf("config: %w", err)
	}
	api, _ := cfg["api"].(map[string]any)
	apiAddr, _ := api["listen"].(string)
	if apiAddr == "" {
		return nil, "", errors.New("config has no api.listen")
	}
	inbounds, _ := cfg["inbounds"].([]any)
	for _, raw := range inbounds {
		in, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		tag, _ := in["tag"].(string)
		si := st.Inbound(tag)
		settings, ok := in["settings"].(map[string]any)
		if si == nil || !ok {
			continue
		}
		clients := make([]any, 0, len(si.Users))
		for _, u := range si.Users {
			clients = append(clients, xrayconf.ClientJSON(xrayconf.Client{Email: u.Email, ID: u.ID}, si.Protocol, si.Flow))
		}
		settings["clients"] = clients
	}
	out, err := json.Marshal(cfg)
	return out, apiAddr, err
}

func findUser(us []state.User, email string) int {
	for i, u := range us {
		if u.Email == email {
			return i
		}
	}
	return -1
}

func short(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

// certLoop renews nginx's certificates and copies them for Xray, which re-reads its certificate
// files every hour by itself. When a domain gets its first real certificate (it ran on a
// self-signed placeholder until then), Xray is restarted at once instead of an hour later.
func (a *Agent) certLoop(ctx context.Context) {
	t := time.NewTicker(a.cfg.CertInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-a.certKick:
		}
		// May take minutes (Let's Encrypt): outside the agent lock. Renewals call OnCerts.
		if err := a.nginx.RenewCerts(ctx); err != nil {
			a.log.Warn("certificates", "err", err)
		}
		a.mu.Lock()
		a.syncCertsLocked(ctx)
		a.mu.Unlock()
		a.notifyCerts()
	}
}

func (a *Agent) syncCertsLocked(ctx context.Context) {
	restart := false
	for _, d := range a.certDoms {
		changed, err := a.certs.sync(d)
		if err != nil {
			a.log.Warn("cannot sync certificate", "domain", d, "err", err)
			continue
		}
		if changed && !a.certs.pending(d) {
			a.log.Info("certificate updated, restarting xray", "domain", d)
			restart = true
		}
	}
	if restart && a.cur != nil {
		if err := a.restartLocked(ctx, a.cur); err != nil {
			a.log.Error("cannot restart xray with the new certificate", "err", err)
		}
	}
}

func (a *Agent) collectLoop(ctx context.Context) {
	t := time.NewTicker(a.cfg.StatsInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.mu.Lock()
			a.collectLocked(ctx)
			a.mu.Unlock()
		}
	}
}

// collectLocked reads and resets Xray counters, samples host metrics and queues a batch.
func (a *Agent) collectLocked(ctx context.Context) {
	b := &nodev1.StatsBatch{Ts: time.Now().Unix(), Metrics: a.metrics.Sample()}
	if a.api != nil && a.proc.Running() {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		users, err := a.api.UserTrafficDeltas(cctx)
		if err != nil {
			a.log.Warn("cannot read user stats", "err", err)
		}
		for _, u := range users {
			b.Users = append(b.Users, &nodev1.UserTraffic{Email: u.Email, Up: u.Uplink, Down: u.Downlink})
		}
		if b.NodeUp, b.NodeDown, err = a.api.InboundTrafficDeltas(cctx); err != nil {
			a.log.Warn("cannot read inbound stats", "err", err)
		}
		// Who is online comes from onlineLoop's last reading (not asked again under the lock).
		if a.online != nil && time.Since(a.onlineAt) < 3*a.cfg.OnlineInterval+5*time.Second {
			for _, o := range a.online.Users {
				b.Online = append(b.Online, &nodev1.OnlineUser{Email: o.Email, Ips: o.Ips})
			}
		}
	}
	if err := a.st.Enqueue(b); err != nil {
		a.log.Error("cannot queue stats", "err", err)
		return
	}
	select {
	case a.statsCh <- struct{}{}:
	default:
	}
}
