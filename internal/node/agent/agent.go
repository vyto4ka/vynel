// Package agent is the node side: it keeps a session with the panel, applies snapshots and
// deltas to Xray and survives panel outages on its saved state (docs/ARCHITECTURE.md §4.2).
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/vyto4ka/vpn/internal/node/state"
	nodev1 "github.com/vyto4ka/vpn/internal/proto/vpn/node/v1"
	"github.com/vyto4ka/vpn/internal/xray"
	"github.com/vyto4ka/vpn/internal/xrayconf"
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
	DataDir    string // state.db and xray config live here
	Xray       xray.Binary
	Version    string
	Dial       Dialer
	Log        *slog.Logger
	Addresses  func() []*nodev1.Address // defaults to the host's interfaces
	MaxBackoff time.Duration            // reconnect backoff cap, default 30s
}

// Agent applies the panel's desired state to Xray.
type Agent struct {
	cfg  Config
	log  *slog.Logger
	st   *state.Store
	proc *xray.Process

	mu      sync.Mutex
	cur     *state.State
	api     *xray.API
	apiAddr string
	xrayVer string
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
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, err
	}
	st, err := state.Open(filepath.Join(cfg.DataDir, "state.db"))
	if err != nil {
		return nil, err
	}
	a := &Agent{cfg: cfg, log: cfg.Log, st: st, proc: xray.NewProcess(cfg.Xray, filepath.Join(cfg.DataDir, "xray.json"), cfg.Log)}
	if a.cur, err = st.Load(); err != nil {
		st.Close()
		return nil, err
	}
	return a, nil
}

// Close stops Xray and closes the state.
func (a *Agent) Close() {
	a.proc.Close()
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

// Run starts Xray from the saved state and keeps a session with the panel until ctx ends.
func (a *Agent) Run(ctx context.Context) error {
	if v, err := a.cfg.Xray.Version(ctx); err == nil {
		a.xrayVer = v
	} else {
		a.log.Warn("cannot get xray version", "err", err)
	}
	a.mu.Lock()
	if a.cur != nil {
		if err := a.restartLocked(ctx, a.cur); err != nil {
			a.log.Error("cannot start xray from saved state", "err", err)
		} else {
			a.log.Info("xray started from saved state", "revision", a.cur.Revision)
		}
	}
	a.mu.Unlock()

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
		if status.Code(err) == codes.PermissionDenied {
			// Revoked or deleted on the panel: retrying fast is pointless.
			backoff = a.cfg.MaxBackoff
			a.log.Error("the panel rejected this node; get a new token and run `vpn node join TOKEN`", "err", err)
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
	hello := &nodev1.Hello{
		AgentVersion: a.cfg.Version, XrayVersion: a.xrayVer, AppliedHash: a.AppliedHash(),
		Os: runtime.GOOS, Arch: runtime.GOARCH, Addresses: a.cfg.Addresses(),
	}
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
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errs:
			return err
		case <-addrTick.C:
			msg := &nodev1.NodeMessage{Msg: &nodev1.NodeMessage_Addresses{Addresses: &nodev1.Addresses{Addresses: a.cfg.Addresses()}}}
			if err := conn.Send(msg); err != nil {
				return err
			}
		case m := <-msgs:
			ack := a.handle(ctx, m)
			if ack == nil {
				continue
			}
			if err := conn.Send(&nodev1.NodeMessage{Msg: &nodev1.NodeMessage_Ack{Ack: ack}}); err != nil {
				return err
			}
		}
	}
}

func (a *Agent) handle(ctx context.Context, m *nodev1.PanelMessage) *nodev1.Ack {
	switch msg := m.Msg.(type) {
	case *nodev1.PanelMessage_Snapshot:
		s := msg.Snapshot
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

// ApplySnapshot makes the node run the snapshot. Same structure: users are changed through the
// Xray API without a restart. Different structure (or API failure): full config and restart.
func (a *Agent) ApplySnapshot(ctx context.Context, s *nodev1.Snapshot) error {
	next := &state.State{Revision: s.Revision, Hash: s.Hash, Config: s.XrayConfig}
	for _, in := range s.Inbounds {
		si := state.Inbound{Tag: in.Tag, Protocol: in.Protocol, Flow: in.Flow}
		for _, u := range in.Users {
			si.Users = append(si.Users, state.User{Email: u.Email, ID: u.Id})
		}
		next.Inbounds = append(next.Inbounds, si)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cur != nil && bytes.Equal(a.cur.Config, next.Config) && a.proc.Running() && a.api != nil {
		err := a.syncUsersLocked(ctx, a.cur, next)
		if err == nil {
			return a.commitLocked(next)
		}
		a.log.Warn("hot user sync failed, restarting xray with the full config", "err", err)
	}
	if err := a.restartLocked(ctx, next); err != nil {
		return err
	}
	return a.commitLocked(next)
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
	for _, op := range d.Ops {
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

func (a *Agent) syncUsersLocked(ctx context.Context, cur, next *state.State) error {
	for _, in := range next.Inbounds {
		old := map[string]string{}
		if ci := cur.Inbound(in.Tag); ci != nil {
			for _, u := range ci.Users {
				old[u.Email] = u.ID
			}
		}
		want := map[string]bool{}
		for _, u := range in.Users {
			want[u.Email] = true
			id, ok := old[u.Email]
			if ok && id == u.ID {
				continue
			}
			if ok {
				if err := a.api.RemoveUser(ctx, in.Tag, u.Email); err != nil {
					return err
				}
			}
			if err := a.addUser(ctx, &in, u); err != nil {
				return err
			}
		}
		for email := range old {
			if !want[email] {
				if err := a.api.RemoveUser(ctx, in.Tag, email); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (a *Agent) addUser(ctx context.Context, in *state.Inbound, u state.User) error {
	switch in.Protocol {
	case "vless":
		return a.api.AddVLESSUser(ctx, in.Tag, u.Email, u.ID, in.Flow)
	}
	return fmt.Errorf("protocol %s is not supported for hot user changes", in.Protocol)
}

// restartLocked writes the full config (structure + clients) and restarts Xray.
func (a *Agent) restartLocked(ctx context.Context, st *state.State) error {
	full, apiAddr, err := FullConfig(st)
	if err != nil {
		return err
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
			clients = append(clients, xrayconf.ClientJSON(xrayconf.Client{Email: u.Email, ID: u.ID}, si.Flow))
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
