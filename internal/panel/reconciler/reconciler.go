// Package reconciler keeps every connected node in its desired state (docs/ARCHITECTURE.md §6.2).
//
// It is level-triggered: on any change it recomputes the desired state of all nodes and, per
// connected node, sends either a Delta (only users changed) or a full Snapshot (structure changed
// or the node's state is unknown). One message is in flight per node; the next one waits for its Ack.
package reconciler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/vyto4ka/vpn/internal/panel/service"
	"github.com/vyto4ka/vpn/internal/panel/store"
	nodev1 "github.com/vyto4ka/vpn/internal/proto/vpn/node/v1"
	"github.com/vyto4ka/vpn/internal/xrayconf"
)

// Stream is the panel side of a node connection: a gRPC server stream or an in-memory pipe.
type Stream interface {
	Context() context.Context
	Send(*nodev1.PanelMessage) error
	Recv() (*nodev1.NodeMessage, error)
}

// Reconciler pushes desired state to nodes.
type Reconciler struct {
	svc *service.Service
	log *slog.Logger

	// Tunables (tests shorten them).
	Debounce      time.Duration // coalesce bursts of changes
	PollInterval  time.Duration // outbox poll for changes made by other processes (CLI)
	StatusEvery   time.Duration // re-evaluate time-based user statuses
	AckTimeout    time.Duration // resend if a node does not answer
	RetryAfterErr time.Duration // back off after a node reports an apply error

	kick chan struct{}

	mu       sync.Mutex
	desired  map[int64]*service.DesiredState
	revision map[int64]int64
	sessions map[int64]*session
	ready    chan struct{} // closed after the first recompute
}

// New creates a reconciler.
func New(svc *service.Service, log *slog.Logger) *Reconciler {
	if log == nil {
		log = slog.Default()
	}
	return &Reconciler{
		svc: svc, log: log,
		Debounce: 300 * time.Millisecond, PollInterval: time.Second, StatusEvery: time.Minute,
		AckTimeout: time.Minute, RetryAfterErr: 5 * time.Second,
		kick:     make(chan struct{}, 1),
		desired:  map[int64]*service.DesiredState{},
		revision: map[int64]int64{},
		sessions: map[int64]*session{},
		ready:    make(chan struct{}),
	}
}

// Notify schedules a recompute (cheap, non-blocking). Hook it to service.OnChange.
func (r *Reconciler) Notify() {
	select {
	case r.kick <- struct{}{}:
	default:
	}
}

// Run recomputes on changes until ctx ends.
func (r *Reconciler) Run(ctx context.Context) error {
	lastEvent, err := store.LastEventID(ctx, r.svc.Store().DB)
	if err != nil {
		return err
	}
	if err := r.recompute(ctx); err != nil {
		r.log.Error("initial recompute failed", "err", err)
	}
	close(r.ready)
	poll := time.NewTicker(r.PollInterval)
	defer poll.Stop()
	statuses := time.NewTicker(r.StatusEvery)
	defer statuses.Stop()
	lastPrune := -1
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-r.kick:
			// Debounce: wait a little and swallow the kicks that arrive meanwhile.
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(r.Debounce):
			}
			select {
			case <-r.kick:
			default:
			}
		case <-poll.C:
			id, err := store.LastEventID(ctx, r.svc.Store().DB)
			if err != nil || id == lastEvent {
				continue
			}
		case <-statuses.C:
			// Expiry, traffic resets and (daily) pruning. Status changes kick us via OnChange.
			day := time.Now().YearDay()
			if err := r.svc.Maintenance(ctx, day != lastPrune); err != nil {
				r.log.Error("maintenance", "err", err)
			} else {
				lastPrune = day
			}
			continue
		}
		if id, err := store.LastEventID(ctx, r.svc.Store().DB); err == nil {
			lastEvent = id
		}
		if err := r.recompute(ctx); err != nil {
			r.log.Error("recompute failed", "err", err)
		}
	}
}

func (r *Reconciler) recompute(ctx context.Context) error {
	states, err := r.svc.DesiredStates(ctx)
	if err != nil {
		return err
	}
	nodes, err := r.svc.Nodes(ctx)
	if err != nil {
		return err
	}
	revs := map[int64]int64{}
	valid := map[int64]bool{} // nodes allowed to stay connected
	for _, n := range nodes {
		valid[n.ID] = n.Local || n.CertSerial != ""
		ds := states[n.ID]
		if ds == nil {
			continue
		}
		rev := n.DesiredRevision
		if ds.Hash != n.DesiredHash {
			rev++
			if err := store.SetNodeDesired(ctx, r.svc.Store().DB, n.ID, rev, ds.Hash); err != nil {
				return err
			}
			r.log.Info("desired state changed", "node", n.Code, "revision", rev, "inbounds", len(ds.Inbounds))
			for _, p := range ds.Problems {
				r.log.Warn("node config problem", "node", n.Code, "problem", p)
			}
		}
		revs[n.ID] = rev
	}
	r.mu.Lock()
	r.desired, r.revision = states, revs
	sessions := make([]*session, 0, len(r.sessions))
	for id, s := range r.sessions {
		if !valid[id] {
			// Node deleted or its certificate revoked: drop the live session too.
			r.log.Warn("closing session of a deleted or revoked node", "node", id)
			s.cancel()
			continue
		}
		sessions = append(sessions, s)
	}
	r.mu.Unlock()
	for _, s := range sessions {
		s.wake()
	}
	return nil
}

// Connected reports whether a node has a live session.
func (r *Reconciler) Connected(nodeID int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.sessions[nodeID]
	return ok
}

// session is one live connection of a node.
type session struct {
	r      *Reconciler
	nodeID int64
	stream Stream
	wakeCh chan struct{}
	out    chan *nodev1.PanelMessage // replies (stats acks) sent by the push loop
	cancel context.CancelFunc

	mu        sync.Mutex
	applied   *service.DesiredState // what the node runs, when known exactly
	appliedH  string                // hash the node reports
	pending   *service.DesiredState // sent, waiting for Ack
	pendingAt time.Time
	holdUntil time.Time
	hello     *nodev1.Hello
}

func (s *session) wake() {
	select {
	case s.wakeCh <- struct{}{}:
	default:
	}
}

// Serve runs a node session until the stream ends. A newer session of the same node closes
// the older one.
func (r *Reconciler) Serve(nodeID int64, stream Stream) error {
	<-r.ready
	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()
	s := &session{r: r, nodeID: nodeID, stream: stream, wakeCh: make(chan struct{}, 1), out: make(chan *nodev1.PanelMessage, 64), cancel: cancel}

	r.mu.Lock()
	if old, ok := r.sessions[nodeID]; ok {
		r.log.Info("node reconnected, closing the previous session", "node", nodeID)
		old.cancel()
	}
	r.sessions[nodeID] = s
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		if r.sessions[nodeID] == s {
			delete(r.sessions, nodeID)
		}
		r.mu.Unlock()
	}()

	msgs := make(chan *nodev1.NodeMessage)
	errs := make(chan error, 1)
	go func() {
		for {
			msg, err := stream.Recv()
			if err != nil {
				errs <- err
				return
			}
			select {
			case msgs <- msg:
			case <-ctx.Done():
				return
			}
		}
	}()
	go s.pushLoop(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-errs:
			if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		case msg := <-msgs:
			if err := s.handle(ctx, msg); err != nil {
				r.log.Error("node message", "node", nodeID, "err", err)
			}
		}
	}
}

func (s *session) handle(ctx context.Context, msg *nodev1.NodeMessage) error {
	svc := s.r.svc
	switch m := msg.Msg.(type) {
	case *nodev1.NodeMessage_Hello:
		h := m.Hello
		s.r.log.Info("node connected", "node", s.nodeID, "agent", h.AgentVersion, "xray", h.XrayVersion)
		if err := svc.SyncAddresses(ctx, s.nodeID, toReported(h.Addresses)); err != nil {
			return err
		}
		s.mu.Lock()
		s.hello, s.appliedH, s.applied, s.pending = h, h.AppliedHash, nil, nil
		if ds := s.r.desiredFor(s.nodeID); ds != nil && ds.Hash == h.AppliedHash {
			s.applied = ds
		}
		s.mu.Unlock()
		if err := store.SetNodeRuntime(ctx, svc.Store().DB, s.nodeID, store.NodeRuntime{AgentVersion: h.AgentVersion, XrayVersion: h.XrayVersion, AppliedHash: h.AppliedHash}); err != nil {
			return err
		}
		if err := store.SetNodeFacts(ctx, svc.Store().DB, s.nodeID, h.CaddyVersion, h.Warnings); err != nil {
			return err
		}
		for _, w := range h.Warnings {
			s.r.log.Warn("node warning", "node", s.nodeID, "warning", w)
		}
	case *nodev1.NodeMessage_Addresses:
		return svc.SyncAddresses(ctx, s.nodeID, toReported(m.Addresses.Addresses))
	case *nodev1.NodeMessage_Stats:
		b := m.Stats
		if err := svc.IngestStats(ctx, s.nodeID, b); err != nil {
			return err // no ack: the node resends the batch
		}
		select {
		case s.out <- &nodev1.PanelMessage{Msg: &nodev1.PanelMessage_StatsAck{StatsAck: &nodev1.StatsAck{Epoch: b.Epoch, Seq: b.Seq}}}:
		case <-ctx.Done():
		}
		return nil
	case *nodev1.NodeMessage_Ack:
		a := m.Ack
		s.mu.Lock()
		if s.pending == nil || s.pending.Hash != a.Hash {
			s.mu.Unlock()
			return nil // stale ack
		}
		if a.Error == "" {
			s.applied, s.appliedH = s.pending, a.Hash
		} else {
			// Unknown state now: the next push is a full snapshot after a pause.
			s.applied, s.appliedH = nil, ""
			s.holdUntil = time.Now().Add(s.r.RetryAfterErr)
			time.AfterFunc(s.r.RetryAfterErr, s.wake)
			s.r.log.Warn("node failed to apply state", "node", s.nodeID, "revision", a.Revision, "err", a.Error)
		}
		s.pending = nil
		var h *nodev1.Hello
		if s.hello != nil {
			h = s.hello
		}
		applied := s.appliedH
		s.mu.Unlock()
		rt := store.NodeRuntime{AppliedHash: applied, LastError: a.Error}
		if h != nil {
			rt.AgentVersion, rt.XrayVersion = h.AgentVersion, h.XrayVersion
		}
		if err := store.SetNodeRuntime(ctx, svc.Store().DB, s.nodeID, rt); err != nil {
			return err
		}
	}
	s.wake()
	return nil
}

func (r *Reconciler) desiredFor(nodeID int64) *service.DesiredState {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.desired[nodeID]
}

func (s *session) pushLoop(ctx context.Context) {
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case reply := <-s.out:
			// Only this goroutine sends on the stream.
			if err := s.stream.Send(reply); err != nil {
				s.r.log.Warn("send to node failed", "node", s.nodeID, "err", err)
				return
			}
			continue
		case <-s.wakeCh:
		case <-tick.C:
		}
		msg := s.next()
		if msg == nil {
			continue
		}
		if err := s.stream.Send(msg); err != nil {
			s.r.log.Warn("send to node failed", "node", s.nodeID, "err", err)
			return
		}
	}
}

// next decides what to send, if anything.
func (s *session) next() *nodev1.PanelMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hello == nil {
		return nil // wait for Hello
	}
	now := time.Now()
	if s.pending != nil {
		if now.Sub(s.pendingAt) < s.r.AckTimeout {
			return nil
		}
		s.r.log.Warn("node did not acknowledge, resending a snapshot", "node", s.nodeID)
		s.pending, s.applied = nil, nil
	}
	if now.Before(s.holdUntil) {
		return nil
	}
	s.r.mu.Lock()
	ds, rev := s.r.desired[s.nodeID], s.r.revision[s.nodeID]
	s.r.mu.Unlock()
	if ds == nil || ds.Hash == s.appliedH {
		return nil
	}
	s.pending, s.pendingAt = ds, now
	if s.applied != nil && bytes.Equal(s.applied.Config, ds.Config) && bytes.Equal(s.applied.Caddy, ds.Caddy) {
		return &nodev1.PanelMessage{Msg: &nodev1.PanelMessage_Delta{Delta: Delta(s.applied, ds, rev)}}
	}
	return &nodev1.PanelMessage{Msg: &nodev1.PanelMessage_Snapshot{Snapshot: Snapshot(ds, rev)}}
}

// Snapshot converts a desired state to the wire format.
func Snapshot(ds *service.DesiredState, rev int64) *nodev1.Snapshot {
	snap := &nodev1.Snapshot{Revision: rev, Hash: ds.Hash, XrayConfig: ds.Config, CaddyConfig: ds.Caddy}
	for _, in := range ds.Inbounds {
		iu := &nodev1.InboundUsers{Tag: in.Tag, Protocol: in.Protocol, Flow: in.Flow}
		for _, c := range ds.Users[in.Tag] {
			iu.Users = append(iu.Users, &nodev1.User{Email: c.Email, Id: c.ID})
		}
		snap.Inbounds = append(snap.Inbounds, iu)
	}
	return snap
}

// Delta lists the user operations turning from into to (same structure).
func Delta(from, to *service.DesiredState, rev int64) *nodev1.Delta {
	d := &nodev1.Delta{Revision: rev, BaseHash: from.Hash, Hash: to.Hash}
	flows := map[string]string{}
	for _, in := range to.Inbounds {
		flows[in.Tag] = in.Flow
	}
	for _, in := range to.Inbounds {
		old := index(from.Users[in.Tag])
		cur := index(to.Users[in.Tag])
		for _, c := range to.Users[in.Tag] {
			if id, ok := old[c.Email]; !ok || id != c.ID {
				d.Ops = append(d.Ops, &nodev1.UserOp{Kind: nodev1.UserOp_KIND_UPSERT, Tag: in.Tag, Flow: flows[in.Tag], User: &nodev1.User{Email: c.Email, Id: c.ID}})
			}
		}
		for _, c := range from.Users[in.Tag] {
			if _, ok := cur[c.Email]; !ok {
				d.Ops = append(d.Ops, &nodev1.UserOp{Kind: nodev1.UserOp_KIND_REMOVE, Tag: in.Tag, User: &nodev1.User{Email: c.Email}})
			}
		}
	}
	return d
}

func index(cs []xrayconf.Client) map[string]string {
	m := make(map[string]string, len(cs))
	for _, c := range cs {
		m[c.Email] = c.ID
	}
	return m
}

func toReported(as []*nodev1.Address) []service.ReportedAddress {
	out := make([]service.ReportedAddress, 0, len(as))
	for _, a := range as {
		out = append(out, service.ReportedAddress{IP: a.Ip, Interface: a.Interface, Primary: a.Primary})
	}
	return out
}

// String helps logging.
func (s *session) String() string { return fmt.Sprintf("node-%d", s.nodeID) }
