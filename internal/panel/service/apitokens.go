package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/store"
)

// API tokens let other programs (billing, bots, other panels) call the public API. A token is
// 256 random bits with a recognizable prefix, so a leaked one is found by secret scanners; the
// panel keeps its hash only, and shows the token once, when it is made.

// TokenPrefix starts every API token.
const TokenPrefix = "vyn_"

// API scopes: what a token may do. A token gets the few it needs.
const (
	ScopeUsersRead     = "users:read"
	ScopeUsersWrite    = "users:write" // create, change, extend, reset, reissue links
	ScopeUsersDelete   = "users:delete"
	ScopeGroupsRead    = "groups:read"
	ScopeTemplatesRead = "templates:read"
	ScopeNodesRead     = "nodes:read"
	ScopeStatsRead     = "stats:read"
	ScopeEventsRead    = "events:read"
)

// APIScopes lists every scope, in the order the panel shows them.
var APIScopes = []string{ScopeUsersRead, ScopeUsersWrite, ScopeUsersDelete, ScopeGroupsRead, ScopeTemplatesRead, ScopeNodesRead, ScopeStatsRead, ScopeEventsRead}

// Why a token was refused. The first is for any token that does not exist: it says nothing else.
var (
	ErrTokenInvalid = errors.New("invalid api token")
	ErrTokenExpired = errors.New("api token expired")
	ErrTokenRevoked = errors.New("api token revoked")
	ErrTokenIP      = errors.New("api token is not allowed from this address")
)

// DefaultAPIRate is the requests per minute of a token that has no limit of its own.
const DefaultAPIRate = 600

// APITokenInput creates or edits a token.
type APITokenInput struct {
	Name      string
	Scopes    []string
	CIDRs     []string   // networks the token may come from; none = anywhere
	RateLimit int        // requests per minute; 0 = DefaultAPIRate
	ExpiresAt *time.Time // nil = never
}

func hashAPIToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// normalizeCIDRs checks networks; a bare address is a network of one.
func normalizeCIDRs(in []string) ([]string, error) {
	out := []string{}
	for _, c := range in {
		if c = strings.TrimSpace(c); c == "" {
			continue
		}
		if ip := net.ParseIP(c); ip != nil {
			if ip.To4() != nil {
				c += "/32"
			} else {
				c += "/128"
			}
		}
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			return nil, invalid("«%s» — не адрес и не сеть (1.2.3.4 или 10.0.0.0/24)", c)
		}
		out = append(out, n.String())
	}
	return out, nil
}

func (in *APITokenInput) check(now time.Time) error {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len([]rune(in.Name)) > 64 {
		return invalid("название токена: от 1 до 64 символов")
	}
	if len(in.Scopes) == 0 {
		return invalid("выберите хотя бы одно право для токена")
	}
	seen := map[string]bool{}
	scopes := []string{}
	for _, sc := range in.Scopes {
		known := false
		for _, k := range APIScopes {
			known = known || k == sc
		}
		if !known {
			return invalid("неизвестное право «%s»", sc)
		}
		if !seen[sc] {
			seen[sc] = true
			scopes = append(scopes, sc)
		}
	}
	in.Scopes = scopes
	var err error
	if in.CIDRs, err = normalizeCIDRs(in.CIDRs); err != nil {
		return err
	}
	if in.RateLimit < 0 || in.RateLimit > 100000 {
		return invalid("лимит запросов: от 0 (по умолчанию) до 100000 в минуту")
	}
	if in.ExpiresAt != nil && !in.ExpiresAt.After(now) {
		return invalid("срок действия токена уже прошёл")
	}
	return nil
}

// CreateAPIToken makes a token. The secret is returned here and never again.
func (s *Service) CreateAPIToken(ctx context.Context, actor Actor, in APITokenInput) (*store.APIToken, string, error) {
	if err := in.check(s.now()); err != nil {
		return nil, "", err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, "", err
	}
	secret := TokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
	t := &store.APIToken{Name: in.Name, Prefix: secret[:len(TokenPrefix)+4], Hash: hashAPIToken(secret), Scopes: in.Scopes, CIDRs: in.CIDRs,
		RateLimit: in.RateLimit, CreatedBy: actor.Kind + ":" + actor.ID}
	if in.ExpiresAt != nil {
		v := in.ExpiresAt.Unix()
		t.ExpiresAt = &v
	}
	err := s.mutate(ctx, change{actor: actor, action: "api_token.create", entity: "api_token", entityID: idOf(&t.ID),
		diff: map[string]any{"name": t.Name, "scopes": t.Scopes, "cidrs": t.CIDRs}},
		func(q store.DBTX) error { return store.CreateAPIToken(ctx, q, t) })
	if err != nil {
		return nil, "", err
	}
	return t, secret, nil
}

// UpdateAPIToken changes what a token may do. The secret stays.
func (s *Service) UpdateAPIToken(ctx context.Context, actor Actor, id int64, in APITokenInput) (*store.APIToken, error) {
	if err := in.check(s.now()); err != nil {
		return nil, err
	}
	var t *store.APIToken
	err := s.mutate(ctx, change{actor: actor, action: "api_token.update", entity: "api_token", entityID: func() int64 { return id },
		diff: map[string]any{"name": in.Name, "scopes": in.Scopes, "cidrs": in.CIDRs}},
		func(q store.DBTX) error {
			var err error
			if t, err = store.GetAPIToken(ctx, q, id); err != nil {
				return err
			}
			t.Name, t.Scopes, t.CIDRs, t.RateLimit, t.ExpiresAt = in.Name, in.Scopes, in.CIDRs, in.RateLimit, nil
			if in.ExpiresAt != nil {
				v := in.ExpiresAt.Unix()
				t.ExpiresAt = &v
			}
			return store.UpdateAPIToken(ctx, q, t)
		})
	return t, err
}

// RevokeAPIToken switches a token off at once; it stays in the list.
func (s *Service) RevokeAPIToken(ctx context.Context, actor Actor, id int64) error {
	return s.mutate(ctx, change{actor: actor, action: "api_token.revoke", entity: "api_token", entityID: func() int64 { return id }},
		func(q store.DBTX) error { return store.RevokeAPIToken(ctx, q, id) })
}

// DeleteAPIToken removes a token from the list.
func (s *Service) DeleteAPIToken(ctx context.Context, actor Actor, id int64) error {
	return s.mutate(ctx, change{actor: actor, action: "api_token.delete", entity: "api_token", entityID: func() int64 { return id }},
		func(q store.DBTX) error { return store.DeleteAPIToken(ctx, q, id) })
}

// APITokens lists the tokens.
func (s *Service) APITokens(ctx context.Context) ([]*store.APIToken, error) {
	return store.ListAPITokens(ctx, s.st.DB)
}

// apiTouch throttles the "last used" writes: a busy integration would write on every request.
type apiTouch struct {
	at   time.Time
	ip   string
	uses int64
}

const apiTouchEvery = 30 * time.Second

// HasScope says whether the token may do what the scope names.
func HasScope(t *store.APIToken, scope string) bool {
	for _, s := range t.Scopes {
		if subtle.ConstantTimeCompare([]byte(s), []byte(scope)) == 1 {
			return true
		}
	}
	return false
}

// AuthenticateAPIToken finds the token a request came with and checks it may be used from ip.
func (s *Service) AuthenticateAPIToken(ctx context.Context, secret, ip string) (*store.APIToken, error) {
	if !strings.HasPrefix(secret, TokenPrefix) || len(secret) > 128 {
		return nil, ErrTokenInvalid
	}
	t, err := store.GetAPITokenByHash(ctx, s.st.DB, hashAPIToken(secret))
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrTokenInvalid
	}
	if err != nil {
		return nil, err
	}
	now := s.now()
	switch {
	case t.RevokedAt != nil:
		return nil, ErrTokenRevoked
	case t.ExpiresAt != nil && now.Unix() >= *t.ExpiresAt:
		return nil, ErrTokenExpired
	case !ipAllowed(t.CIDRs, ip):
		return nil, ErrTokenIP
	}
	s.touchAPIToken(ctx, t, ip, now)
	return t, nil
}

func ipAllowed(cidrs []string, ip string) bool {
	if len(cidrs) == 0 {
		return true
	}
	addr := net.ParseIP(ip)
	if addr == nil {
		return false
	}
	for _, c := range cidrs {
		if _, n, err := net.ParseCIDR(c); err == nil && n.Contains(addr) {
			return true
		}
	}
	return false
}

func (s *Service) touchAPIToken(ctx context.Context, t *store.APIToken, ip string, now time.Time) {
	s.api.mu.Lock()
	if s.api.touch == nil {
		s.api.touch = map[int64]*apiTouch{}
	}
	st := s.api.touch[t.ID]
	if st == nil {
		st = &apiTouch{}
		s.api.touch[t.ID] = st
	}
	st.uses++
	flush := now.Sub(st.at) >= apiTouchEvery || st.ip != ip
	uses := st.uses
	if flush {
		st.at, st.ip, st.uses = now, ip, 0
	}
	s.api.mu.Unlock()
	if flush {
		_ = store.TouchAPIToken(ctx, s.st.DB, t.ID, ip, uses)
	}
}

// IdempotentReply returns the saved answer to a request that carried this key before (nil = none).
func (s *Service) IdempotentReply(ctx context.Context, tokenID int64, key string) (*store.Idempotent, error) {
	r, err := store.GetIdempotent(ctx, s.st.DB, tokenID, key)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	return r, err
}

// SaveIdempotentReply keeps the answer to a request for repeats.
func (s *Service) SaveIdempotentReply(ctx context.Context, tokenID int64, key string, r store.Idempotent) error {
	return store.PutIdempotent(ctx, s.st.DB, tokenID, key, r)
}

type apiState struct {
	mu    sync.Mutex
	touch map[int64]*apiTouch

	nodesUp  map[int64]int // node id → polls in a row that agree with seen
	nodeSeen map[int64]bool
	nodeSent map[int64]bool
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
