// Package service holds the panel's business logic. Every transport (CLI, web, bot, future API)
// calls these methods; they never touch the store directly (docs/ARCHITECTURE.md §4.1, §12).
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/store"
)

// ErrInvalid marks input validation failures.
var ErrInvalid = errors.New("invalid input")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// Re-exported store errors so transports do not import store.
var (
	ErrNotFound = store.ErrNotFound
	ErrConflict = store.ErrConflict
)

// Actor identifies who performs an action (audit log).
type Actor struct {
	Kind string // admin|bot|cli|system|api
	ID   string
}

// Common actors.
var (
	ActorSystem = Actor{Kind: "system"}
	ActorCLI    = Actor{Kind: "cli"}
)

// Event types written to the outbox.
const (
	EvNodeChanged    = "node.changed"
	EvNodeDeleted    = "node.deleted"
	EvProfileChanged = "profile.changed"
	EvInboundChanged = "inbound.changed"
	EvGroupChanged   = "group.changed"
	EvUserChanged    = "user.changed"
	EvUserDeleted    = "user.deleted"
	EvSettingChanged = "setting.changed"
)

// Service is the entry point to the business logic.
type Service struct {
	st  *store.Store
	now func() time.Time
	tpl templateCache
	// OnChange is called after every committed change (the reconciler hooks in here for instant pushes).
	OnChange func()

	noticeOnce sync.Once
	notices    chan Notice
}

// New creates a service over a store.
func New(st *store.Store) *Service {
	return &Service{st: st, now: time.Now}
}

// Store exposes the underlying store for read-heavy components (reconciler, gateway).
func (s *Service) Store() *store.Store { return s.st }

// SetClock replaces the clock (tests).
func (s *Service) SetClock(now func() time.Time) {
	s.now = now
	store.Now = now
}

// change runs fn in a transaction, then records the audit entry and the outbox event in the same
// transaction, and finally notifies OnChange.
type change struct {
	actor    Actor
	action   string // audit action, e.g. user.create
	entity   string
	entityID func() int64
	event    string
	diff     any
}

func (s *Service) mutate(ctx context.Context, c change, fn func(q store.DBTX) error) error {
	if err := s.syncTemplates(ctx); err != nil {
		return err
	}
	err := s.st.Tx(ctx, func(q store.DBTX) error {
		if err := fn(q); err != nil {
			return err
		}
		var id int64
		if c.entityID != nil {
			id = c.entityID()
		}
		diff := "{}"
		if c.diff != nil {
			b, err := json.Marshal(c.diff)
			if err != nil {
				return err
			}
			diff = string(b)
		}
		if err := store.AddAudit(ctx, q, store.AuditEntry{
			Actor: c.actor.Kind, ActorID: c.actor.ID, Action: c.action, Entity: c.entity, EntityID: id, Diff: diff,
		}); err != nil {
			return err
		}
		if c.event != "" {
			return store.AddEvent(ctx, q, c.event, id, nil)
		}
		return nil
	})
	if err == nil && s.OnChange != nil {
		s.OnChange()
	}
	return err
}

func idOf(p *int64) func() int64 { return func() int64 { return *p } }

// Settings keys.
const (
	SettingXrayAPIPort = "node.xray_api_port" // Xray API port on every node (127.0.0.1)
	SettingGatewayAddr = "gateway.addr"       // host:port nodes dial (goes into join tokens)
	SettingGatewaySNI  = "gateway.sni"        // required TLS server name of the gateway
)

// Setting returns a setting or its default.
func (s *Service) Setting(ctx context.Context, key, def string) (string, error) {
	v, ok, err := store.GetSetting(ctx, s.st.DB, key)
	if err != nil || !ok {
		return def, err
	}
	return v, nil
}

// SetSetting stores a setting.
func (s *Service) SetSetting(ctx context.Context, actor Actor, key, value string) error {
	return s.mutate(ctx, change{actor: actor, action: "setting.set", entity: "setting", event: EvSettingChanged, diff: map[string]string{key: value}},
		func(q store.DBTX) error { return store.SetSetting(ctx, q, key, value) })
}
