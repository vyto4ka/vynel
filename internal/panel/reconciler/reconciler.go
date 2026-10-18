// Package reconciler keeps every connected node in its desired state (docs/ARCHITECTURE.md §6.2).
//
// It is level-triggered: on any change it recomputes the desired state of all nodes and, per
// connected node, sends either a Delta (only users changed) or a full Snapshot (structure changed
// or the node's state is unknown). One message is in flight per node; the next one waits for its Ack.
package reconciler

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
	nodev1 "github.com/vyto4ka/vynel/internal/proto/vynel/node/v1"
	"github.com/vyto4ka/vynel/internal/update"
	"github.com/vyto4ka/vynel/internal/xrayconf"
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

	recomputeMu sync.Mutex // the loop and a node's Hello may recompute at once
	mu          sync.Mutex
	desired     map[int64]*service.DesiredState
	revision    map[int64]int64
	sessions    map[int64]*session
	ready       chan struct{}            // closed after the first recompute
	updates     map[int64]*update.Status // what each node last said about its update
	blocks      *nodev1.IPBlocks         // addresses over users' limits, for every node
	blocksVer   int64
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
		updates:  map[int64]*update.Status{},
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
	// Slots of addresses that went away free up even when no node reports.
	ipTick := time.NewTicker(5 * time.Second)
	defer ipTick.Stop()
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
			// Possibly a change made by another process (the CLI): drop the cached configuration.
			r.svc.Invalidate()
		case <-ipTick.C:
			if blocks, changed := r.svc.IPs().Evaluate(); changed {
				r.setBlocks(blocks)
			}
			continue
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
	r.recomputeMu.Lock()
	defer r.recomputeMu.Unlock()
	states, err := r.svc.DesiredStates(ctx)
	if err != nil {
		return err
	}
	if err := r.svc.RefreshIPLimits(ctx); err != nil {
		r.log.Warn("ip limits", "err", err)
	} else if blocks, changed := r.svc.IPs().Evaluate(); changed {
		r.setBlocks(blocks)
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

// CapIPLimit is the capability of agents that report online addresses (OnlineIPs) and apply
// IPBlocks.
const CapIPLimit = "iplimit"

// setBlocks publishes new blocks; every session sends them to its node.
func (r *Reconciler) setBlocks(blocks map[int64][]string) {
	msg := &nodev1.IPBlocks{}
	ids := make([]int64, 0, len(blocks))
	for u := range blocks {
		ids = append(ids, u)
	}
	slices.Sort(ids)
	for _, u := range ids {
		msg.Blocks = append(msg.Blocks, &nodev1.IPBlock{UserId: u, Ips: blocks[u]})
	}
	r.mu.Lock()
	r.blocks = msg
	r.blocksVer++
	sessions := make([]*session, 0, len(r.sessions))
	for _, s := range r.sessions {
		sessions = append(sessions, s)
	}
	r.mu.Unlock()
	if len(msg.Blocks) > 0 {
		r.log.Info("addresses over the limit", "users", len(msg.Blocks))
	}
	for _, s := range sessions {
		s.wake()
	}
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
	waiters   map[string]chan *nodev1.CommandResult // admin commands sent, by id
	blocksVer int64                                 // the IPBlocks version the node has (-1 = none)
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
	s := &session{r: r, nodeID: nodeID, stream: stream, wakeCh: make(chan struct{}, 1), out: make(chan *nodev1.PanelMessage, 64), cancel: cancel, blocksVer: -1}

	r.mu.Lock()
	if old, ok := r.sessions[nodeID]; ok {
		r.log.Info("node reconnected, closing the previous session", "node", nodeID)
		old.cancel()
	}
	r.sessions[nodeID] = s
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		current := r.sessions[nodeID] == s
		if current {
			delete(r.sessions, nodeID)
		}
		r.mu.Unlock()
		if current {
			// Its users are no longer online there.
			r.svc.IPs().Forget(nodeID)
			if blocks, changed := r.svc.IPs().Evaluate(); changed {
				r.setBlocks(blocks)
			}
		}
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
		prev, err := store.GetNode(ctx, svc.Store().DB, s.nodeID)
		if err != nil {
			return err
		}
		facts := store.NodeFacts{CaddyVersion: h.CaddyVersion, NginxVersion: h.NginxVersion, Warnings: h.Warnings, Capabilities: h.Capabilities}
		if err := store.SetNodeFacts(ctx, svc.Store().DB, s.nodeID, facts); err != nil {
			return err
		}
		// What the node can run decides its config (nginx or Caddy). If that changed, the
		// first snapshot must already be built for it, not the old web server.
		if prev.NginxVersion != h.NginxVersion || !slices.Equal(prev.Capabilities, h.Capabilities) {
			if err := s.r.recompute(ctx); err != nil {
				return err
			}
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
		for _, w := range h.Warnings {
			s.r.log.Warn("node warning", "node", s.nodeID, "warning", w)
		}
		s.wake()
		s.r.Notify() // reported addresses may change the desired state too
	case *nodev1.NodeMessage_Addresses:
		return svc.SyncAddresses(ctx, s.nodeID, toReported(m.Addresses.Addresses))
	case *nodev1.NodeMessage_OnlineIps:
		online := make(map[string][]string, len(m.OnlineIps.Users))
		for _, u := range m.OnlineIps.Users {
			online[u.Email] = u.IpList
		}
		if blocks, changed := svc.IPs().Report(s.nodeID, online); changed {
			s.r.setBlocks(blocks)
		}
		return nil
	case *nodev1.NodeMessage_Update:
		u := m.Update
		s.r.mu.Lock()
		s.r.updates[s.nodeID] = &update.Status{Running: u.Running, OK: u.Ok, StartedAt: u.StartedAt, FinishedAt: u.FinishedAt, Version: u.Version, Log: u.Log}
		s.r.mu.Unlock()
		return nil
	case *nodev1.NodeMessage_Result:
		s.mu.Lock()
		w := s.waiters[m.Result.Id]
		delete(s.waiters, m.Result.Id)
		s.mu.Unlock()
		if w != nil {
			w <- m.Result
		}
		return nil
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
		if a.Error == "" {
			s.wake() // a newer state may have been computed while this one was applied
		}
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

// ErrNotConnected: the node has no live session, so it cannot take a command.
var ErrNotConnected = errors.New("the node is not connected")

// Command sends an admin action to a connected node and waits for its answer.
func (r *Reconciler) Command(ctx context.Context, nodeID int64, action nodev1.Command_Action) (*nodev1.CommandResult, error) {
	r.mu.Lock()
	s := r.sessions[nodeID]
	r.mu.Unlock()
	if s == nil {
		return nil, ErrNotConnected
	}
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	id := hex.EncodeToString(b)
	ch := make(chan *nodev1.CommandResult, 1)
	s.mu.Lock()
	if s.waiters == nil {
		s.waiters = map[string]chan *nodev1.CommandResult{}
	}
	s.waiters[id] = ch
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.waiters, id)
		s.mu.Unlock()
	}()
	msg := &nodev1.PanelMessage{Msg: &nodev1.PanelMessage_Command{Command: &nodev1.Command{Id: id, Action: action}}}
	select {
	case s.out <- msg:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case res := <-ch:
		return res, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("no answer from the node: %w", ctx.Err())
	}
}

// UpdateStatus is what the node last reported about its update (nil: nothing, or an agent too
// old to report).
func (r *Reconciler) UpdateStatus(nodeID int64) *update.Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	if st := r.updates[nodeID]; st != nil {
		c := *st
		return &c
	}
	return nil
}

// Resync makes the next push to the node a full snapshot (admin action «send the config again»).
func (r *Reconciler) Resync(nodeID int64) error {
	r.mu.Lock()
	s := r.sessions[nodeID]
	r.mu.Unlock()
	if s == nil {
		return ErrNotConnected
	}
	s.mu.Lock()
	s.applied, s.appliedH, s.pending, s.holdUntil = nil, "", nil, time.Time{}
	s.mu.Unlock()
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
		for _, msg := range append(s.next(), s.nextBlocks()...) {
			if err := s.stream.Send(msg); err != nil {
				s.r.log.Warn("send to node failed", "node", s.nodeID, "err", err)
				return
			}
		}
	}
}

// nextBlocks returns the IP blocks when the node does not have the latest ones yet. A fresh
// session always gets them, empty or not: the node may still apply blocks from before.
func (s *session) nextBlocks() []*nodev1.PanelMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hello == nil || !slices.Contains(s.hello.Capabilities, CapIPLimit) {
		return nil
	}
	s.r.mu.Lock()
	blocks, ver := s.r.blocks, s.r.blocksVer
	s.r.mu.Unlock()
	if ver == s.blocksVer {
		return nil
	}
	s.blocksVer = ver
	if blocks == nil {
		blocks = &nodev1.IPBlocks{}
	}
	return []*nodev1.PanelMessage{{Msg: &nodev1.PanelMessage_IpBlocks{IpBlocks: blocks}}}
}

// next decides what to send, if anything.
func (s *session) next() []*nodev1.PanelMessage {
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
	parts := s.hello != nil && slices.Contains(s.hello.Capabilities, CapChunks)
	if s.applied != nil && bytes.Equal(s.applied.Config, ds.Config) && bytes.Equal(s.applied.Caddy, ds.Caddy) && s.applied.Nginx == ds.Nginx {
		d := Delta(s.applied, ds, rev)
		// A bulk change makes a delta as big as a snapshot; that one can go in parts.
		if !parts || proto.Size(d) <= PartBytes {
			return []*nodev1.PanelMessage{{Msg: &nodev1.PanelMessage_Delta{Delta: d}}}
		}
	}
	if !parts {
		return []*nodev1.PanelMessage{{Msg: &nodev1.PanelMessage_Snapshot{Snapshot: Snapshot(ds, rev)}}}
	}
	var out []*nodev1.PanelMessage
	for _, p := range SnapshotParts(ds, rev, PartBytes) {
		out = append(out, &nodev1.PanelMessage{Msg: &nodev1.PanelMessage_Snapshot{Snapshot: p}})
	}
	return out
}

// Snapshot converts a desired state to the wire format.
func Snapshot(ds *service.DesiredState, rev int64) *nodev1.Snapshot {
	snap := &nodev1.Snapshot{Revision: rev, Hash: ds.Hash, XrayConfig: ds.Config, CaddyConfig: ds.Caddy, NginxConfig: ds.Nginx}
	for _, in := range ds.Inbounds {
		iu := &nodev1.InboundUsers{Tag: in.Tag, Protocol: in.Protocol, Flow: in.Flow}
		for _, c := range ds.Users[in.Tag] {
			iu.Users = append(iu.Users, &nodev1.User{Email: c.Email, Id: c.ID})
		}
		snap.Inbounds = append(snap.Inbounds, iu)
	}
	return snap
}

// CapChunks is the agent capability to take a snapshot in parts (Snapshot.part, parts).
const CapChunks = "chunks"

// PartBytes is the size a snapshot part aims at: far below the gRPC message limit (agents
// without "chunks" still have gRPC's 4 MB) and small enough to keep the stream responsive.
const PartBytes = 1 << 20

// SnapshotParts splits a snapshot for an agent with CapChunks. A small one stays whole
// (parts = 0). Otherwise part 0 carries the configs and every part carries users up to about
// limit bytes; an inbound that does not fit continues at the start of the next part.
func SnapshotParts(ds *service.DesiredState, rev int64, limit int) []*nodev1.Snapshot {
	whole := Snapshot(ds, rev)
	if proto.Size(whole) <= limit {
		return []*nodev1.Snapshot{whole}
	}
	cur := &nodev1.Snapshot{Revision: rev, Hash: ds.Hash, XrayConfig: ds.Config, CaddyConfig: ds.Caddy, NginxConfig: ds.Nginx}
	size := proto.Size(cur)
	out := []*nodev1.Snapshot{cur}
	next := func() {
		cur = &nodev1.Snapshot{Revision: rev, Hash: ds.Hash}
		size = proto.Size(cur)
		out = append(out, cur)
	}
	for _, in := range whole.Inbounds {
		head := func() *nodev1.InboundUsers {
			iu := &nodev1.InboundUsers{Tag: in.Tag, Protocol: in.Protocol, Flow: in.Flow}
			cur.Inbounds = append(cur.Inbounds, iu)
			size += proto.Size(iu) + 8
			return iu
		}
		iu := head()
		for _, u := range in.Users {
			n := proto.Size(u) + 8
			if size+n > limit && len(iu.Users) > 0 {
				next()
				iu = head()
			}
			iu.Users = append(iu.Users, u)
			size += n
		}
	}
	for i, p := range out {
		p.Part, p.Parts = int32(i), int32(len(out))
	}
	return out
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
