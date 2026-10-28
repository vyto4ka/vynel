// Package service holds the panel's business logic. Every transport (CLI, web, bot, future API)
// calls these methods; they never touch the store directly (docs/ARCHITECTURE.md §4.1, §12).
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/vyto4ka/vynel/internal/i18n"
	"github.com/vyto4ka/vynel/internal/panel/store"
)

// ErrInvalid marks input validation failures.
var ErrInvalid = errors.New("invalid input")

// invalidError is an input error that keeps its format and arguments, so the web interface can
// say it in the admin's language (internal/i18n); Error() is the Russian text, as before.
type invalidError struct {
	format string
	args   []any
}

func invalid(format string, args ...any) error { return &invalidError{format, args} }

func (e *invalidError) Is(target error) bool { return target == ErrInvalid }

func (e *invalidError) text() string {
	args := make([]any, len(e.args))
	for i, a := range e.args {
		if ie, ok := a.(*invalidError); ok {
			a = ie.text()
		}
		args[i] = a
	}
	return fmt.Sprintf(e.format, args...)
}

func (e *invalidError) Error() string { return ErrInvalid.Error() + ": " + e.text() }

// Localize is the message without the "invalid input" prefix, in lang.
func (e *invalidError) Localize(lang string) string { return i18n.Sprintf(lang, e.format, e.args...) }

// InField says where an input error is, as the panel names it ("«Маршруты»", "«Всегда
// напрямую», строка 3"): the admin sees which field and line to fix. Other errors pass as they are.
func InField(where string, err error) error {
	if err == nil || !errors.Is(err, ErrInvalid) {
		return err
	}
	var inner *invalidError
	if !errors.As(err, &inner) {
		return invalid("%s: %s", where, strings.TrimPrefix(err.Error(), ErrInvalid.Error()+": "))
	}
	msg := inner.text()
	if strings.HasPrefix(msg, "«") || strings.HasPrefix(msg, "строка ") {
		return invalid("%s, %s", where, inner) // the message starts with a place inside this one
	}
	return invalid("%s: %s", where, inner)
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
	ipOnce     sync.Once
	ips        *IPTracker
	ipLog      ipLog // addresses written for the «Аналитика» page
	bindMu     sync.Mutex
	bindFor    string // the bind code the wrong tries are counted for
	bindFails  int
	grantCheck int64 // when Maintenance last looked for ended grants
	cache      confCache
	snapMu     sync.Mutex // one snapshot builder at a time
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
	if err == nil {
		s.Invalidate()
	}
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
	m, err := s.settingsMap(ctx)
	if err != nil {
		return def, err
	}
	if v, ok := m[key]; ok {
		return v, nil
	}
	return def, nil
}

// SetSetting stores a setting.
func (s *Service) SetSetting(ctx context.Context, actor Actor, key, value string) error {
	return s.mutate(ctx, change{actor: actor, action: "setting.set", entity: "setting", event: EvSettingChanged, diff: map[string]string{key: value}},
		func(q store.DBTX) error { return store.SetSetting(ctx, q, key, value) })
}
