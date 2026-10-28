package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
)

// ---- tokens ----

// APIToken is a key for the public API. Only the hash of the token is stored.
type APIToken struct {
	ID         int64
	Name       string
	Prefix     string
	Hash       string
	Scopes     []string
	CIDRs      []string
	RateLimit  int
	ExpiresAt  *int64
	CreatedBy  string
	CreatedAt  int64
	LastUsedAt *int64
	LastUsedIP string
	Uses       int64
	RevokedAt  *int64
}

const apiTokenCols = `id, name, prefix, hash, scopes, cidrs, rate_limit, expires_at, created_by, created_at, last_used_at, last_used_ip, uses, revoked_at`

func splitList(s string) []string {
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func scanAPIToken(sc interface{ Scan(...any) error }) (*APIToken, error) {
	t := &APIToken{}
	var scopes, cidrs string
	var exp, used, revoked sql.NullInt64
	if err := sc.Scan(&t.ID, &t.Name, &t.Prefix, &t.Hash, &scopes, &cidrs, &t.RateLimit, &exp, &t.CreatedBy, &t.CreatedAt, &used, &t.LastUsedIP, &t.Uses, &revoked); err != nil {
		return nil, mapErr(err)
	}
	t.Scopes, t.CIDRs = splitList(scopes), splitList(cidrs)
	if exp.Valid {
		t.ExpiresAt = &exp.Int64
	}
	if used.Valid {
		t.LastUsedAt = &used.Int64
	}
	if revoked.Valid {
		t.RevokedAt = &revoked.Int64
	}
	return t, nil
}

// CreateAPIToken stores a token.
func CreateAPIToken(ctx context.Context, q DBTX, t *APIToken) error {
	t.CreatedAt = unix()
	res, err := q.ExecContext(ctx, `INSERT INTO api_tokens (name, prefix, hash, scopes, cidrs, rate_limit, expires_at, created_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, t.Name, t.Prefix, t.Hash, strings.Join(t.Scopes, ","), strings.Join(t.CIDRs, ","), t.RateLimit, t.ExpiresAt, t.CreatedBy, t.CreatedAt)
	if err != nil {
		return mapErr(err)
	}
	t.ID, err = res.LastInsertId()
	return err
}

// GetAPIToken loads a token by id.
func GetAPIToken(ctx context.Context, q DBTX, id int64) (*APIToken, error) {
	return scanAPIToken(q.QueryRowContext(ctx, `SELECT `+apiTokenCols+` FROM api_tokens WHERE id=?`, id))
}

// GetAPITokenByHash loads a token by the hash of its secret.
func GetAPITokenByHash(ctx context.Context, q DBTX, hash string) (*APIToken, error) {
	return scanAPIToken(q.QueryRowContext(ctx, `SELECT `+apiTokenCols+` FROM api_tokens WHERE hash=?`, hash))
}

// ListAPITokens returns all tokens, newest first.
func ListAPITokens(ctx context.Context, q DBTX) ([]*APIToken, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+apiTokenCols+` FROM api_tokens ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*APIToken
	for rows.Next() {
		t, err := scanAPIToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// UpdateAPIToken saves what an admin may change on a token.
func UpdateAPIToken(ctx context.Context, q DBTX, t *APIToken) error {
	return execOne(ctx, q, `UPDATE api_tokens SET name=?, scopes=?, cidrs=?, rate_limit=?, expires_at=? WHERE id=?`,
		t.Name, strings.Join(t.Scopes, ","), strings.Join(t.CIDRs, ","), t.RateLimit, t.ExpiresAt, t.ID)
}

// RevokeAPIToken switches a token off for good.
func RevokeAPIToken(ctx context.Context, q DBTX, id int64) error {
	return execOne(ctx, q, `UPDATE api_tokens SET revoked_at=? WHERE id=? AND revoked_at IS NULL`, unix(), id)
}

// DeleteAPIToken removes a token (its idempotency records go with it).
func DeleteAPIToken(ctx context.Context, q DBTX, id int64) error {
	return execOne(ctx, q, `DELETE FROM api_tokens WHERE id=?`, id)
}

// TouchAPIToken records a use of the token.
func TouchAPIToken(ctx context.Context, q DBTX, id int64, ip string, uses int64) error {
	_, err := q.ExecContext(ctx, `UPDATE api_tokens SET last_used_at=?, last_used_ip=?, uses=uses+? WHERE id=?`, unix(), ip, uses, id)
	return err
}

// ---- idempotency ----

// Idempotent is the first answer to a request with an idempotency key.
type Idempotent struct {
	Fingerprint string
	Status      int
	Response    string
}

// GetIdempotent returns the saved answer for a key, or ErrNotFound.
func GetIdempotent(ctx context.Context, q DBTX, tokenID int64, key string) (*Idempotent, error) {
	r := &Idempotent{}
	err := q.QueryRowContext(ctx, `SELECT fingerprint, status, response FROM api_idempotency WHERE token_id=? AND key=? AND created_at > ?`,
		tokenID, key, unix()-IdempotencyTTL).Scan(&r.Fingerprint, &r.Status, &r.Response)
	return r, mapErr(err)
}

// IdempotencyTTL is how long an answer is kept for repeats, in seconds.
const IdempotencyTTL = 24 * 3600

// PutIdempotent saves the answer to a request. A key that is already there is left alone.
func PutIdempotent(ctx context.Context, q DBTX, tokenID int64, key string, r Idempotent) error {
	_, err := q.ExecContext(ctx, `INSERT INTO api_idempotency (token_id, key, fingerprint, status, response, created_at) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (token_id, key) DO UPDATE SET fingerprint=excluded.fingerprint, status=excluded.status, response=excluded.response, created_at=excluded.created_at
		WHERE api_idempotency.created_at <= ?`, tokenID, key, r.Fingerprint, r.Status, r.Response, unix(), unix()-IdempotencyTTL)
	return err
}

// ---- events ----

// APIEvent is something that happened, as the API tells it.
type APIEvent struct {
	ID       int64
	TS       int64
	Type     string
	Entity   string
	EntityID int64
	Actor    string
	Data     string
}

// EventDoc is the JSON of an event, in the feed and in webhook deliveries.
func EventDoc(e *APIEvent) []byte {
	var data, entity json.RawMessage = json.RawMessage(e.Data), nil
	if !json.Valid(data) {
		data = json.RawMessage("{}")
	}
	ent := map[string]any{"type": e.Entity, "id": e.EntityID}
	var d map[string]any
	if json.Unmarshal(data, &d) == nil {
		for _, k := range []string{"username", "name", "code"} {
			if v, ok := d[k]; ok {
				ent[k] = v
				delete(d, k)
			}
		}
		data, _ = json.Marshal(d)
	}
	entity, _ = json.Marshal(ent)
	actorKind, actorID, _ := strings.Cut(e.Actor, ":")
	b, _ := json.Marshal(map[string]any{
		"id": "evt_" + strconv.FormatInt(e.ID, 10), "type": e.Type, "created": e.TS,
		"actor": map[string]string{"kind": actorKind, "id": actorID}, "entity": entity, "data": data,
	})
	return b
}

// ListAPIEvents returns events with id > after, oldest first; types narrows by type or prefix ("user.*").
func ListAPIEvents(ctx context.Context, q DBTX, after int64, types []string, limit int) ([]*APIEvent, error) {
	where, args := []string{"id > ?"}, []any{after}
	if len(types) > 0 {
		var or []string
		for _, t := range types {
			if p, ok := strings.CutSuffix(t, "*"); ok {
				or, args = append(or, "type LIKE ?"), append(args, p+"%")
			} else {
				or, args = append(or, "type = ?"), append(args, t)
			}
		}
		where = append(where, "("+strings.Join(or, " OR ")+")")
	}
	args = append(args, limit)
	rows, err := q.QueryContext(ctx, `SELECT id, ts, type, entity, entity_id, actor, data FROM api_events WHERE `+strings.Join(where, " AND ")+` ORDER BY id LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*APIEvent
	for rows.Next() {
		e := &APIEvent{}
		if err := rows.Scan(&e.ID, &e.TS, &e.Type, &e.Entity, &e.EntityID, &e.Actor, &e.Data); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// LastAPIEventID is the newest event id (0 = none yet).
func LastAPIEventID(ctx context.Context, q DBTX) (int64, error) {
	var id int64
	err := q.QueryRowContext(ctx, `SELECT coalesce(max(id), 0) FROM api_events`).Scan(&id)
	return id, err
}

// PublishAPIEvent writes an event and queues its delivery to every webhook that wants it.
func PublishAPIEvent(ctx context.Context, q DBTX, e *APIEvent) error {
	if e.Data == "" {
		e.Data = "{}"
	}
	e.TS = unix()
	res, err := q.ExecContext(ctx, `INSERT INTO api_events (ts, type, entity, entity_id, actor, data) VALUES (?, ?, ?, ?, ?, ?)`,
		e.TS, e.Type, e.Entity, e.EntityID, e.Actor, e.Data)
	if err != nil {
		return err
	}
	if e.ID, err = res.LastInsertId(); err != nil {
		return err
	}
	hooks, err := ListWebhooks(ctx, q)
	if err != nil {
		return err
	}
	var body string
	for _, h := range hooks {
		if !h.Enabled || !h.Wants(e.Type) {
			continue
		}
		if body == "" {
			body = string(EventDoc(e))
		}
		if _, err := q.ExecContext(ctx, `INSERT INTO webhook_deliveries (webhook_id, event_id, type, body, next_at, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			h.ID, e.ID, e.Type, body, e.TS, e.TS); err != nil {
			return err
		}
	}
	return nil
}

// PruneAPI deletes what is old: events and finished deliveries after keepDays, idempotency records after their time.
func PruneAPI(ctx context.Context, q DBTX, keepDays int) error {
	cut := unix() - int64(keepDays)*86400
	for _, s := range []struct {
		sql string
		arg int64
	}{
		{`DELETE FROM api_events WHERE ts < ?`, cut},
		{`DELETE FROM webhook_deliveries WHERE state <> 'pending' AND created_at < ?`, cut},
		{`DELETE FROM api_idempotency WHERE created_at < ?`, unix() - IdempotencyTTL},
	} {
		if _, err := q.ExecContext(ctx, s.sql, s.arg); err != nil {
			return err
		}
	}
	return nil
}

// ---- webhooks ----

// Webhook is an address that gets events.
type Webhook struct {
	ID           int64
	Name         string
	URL          string
	Secret       string
	Events       []string
	Enabled      bool
	AllowPrivate bool
	Fails        int
	OffReason    string
	CreatedAt    int64
	LastAt       *int64
	LastStatus   int
}

// Wants says whether the webhook subscribed to the event type.
func (w *Webhook) Wants(typ string) bool {
	if len(w.Events) == 0 {
		return true
	}
	for _, e := range w.Events {
		if e == "*" || e == typ {
			return true
		}
		if p, ok := strings.CutSuffix(e, "*"); ok && strings.HasPrefix(typ, p) {
			return true
		}
	}
	return false
}

const webhookCols = `id, name, url, secret, events, enabled, allow_private, fails, off_reason, created_at, last_at, last_status`

func scanWebhook(sc interface{ Scan(...any) error }) (*Webhook, error) {
	w := &Webhook{}
	var events string
	var last sql.NullInt64
	if err := sc.Scan(&w.ID, &w.Name, &w.URL, &w.Secret, &events, &w.Enabled, &w.AllowPrivate, &w.Fails, &w.OffReason, &w.CreatedAt, &last, &w.LastStatus); err != nil {
		return nil, mapErr(err)
	}
	w.Events = splitList(events)
	if last.Valid {
		w.LastAt = &last.Int64
	}
	return w, nil
}

// CreateWebhook stores a webhook.
func CreateWebhook(ctx context.Context, q DBTX, w *Webhook) error {
	w.CreatedAt = unix()
	res, err := q.ExecContext(ctx, `INSERT INTO webhooks (name, url, secret, events, enabled, allow_private, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		w.Name, w.URL, w.Secret, strings.Join(w.Events, ","), w.Enabled, w.AllowPrivate, w.CreatedAt)
	if err != nil {
		return mapErr(err)
	}
	w.ID, err = res.LastInsertId()
	return err
}

// UpdateWebhook saves a webhook; switching it on clears the failure count.
func UpdateWebhook(ctx context.Context, q DBTX, w *Webhook) error {
	return execOne(ctx, q, `UPDATE webhooks SET name=?, url=?, secret=?, events=?, enabled=?, allow_private=?, fails=?, off_reason=? WHERE id=?`,
		w.Name, w.URL, w.Secret, strings.Join(w.Events, ","), w.Enabled, w.AllowPrivate, w.Fails, w.OffReason, w.ID)
}

// GetWebhook loads a webhook.
func GetWebhook(ctx context.Context, q DBTX, id int64) (*Webhook, error) {
	return scanWebhook(q.QueryRowContext(ctx, `SELECT `+webhookCols+` FROM webhooks WHERE id=?`, id))
}

// ListWebhooks returns all webhooks.
func ListWebhooks(ctx context.Context, q DBTX) ([]*Webhook, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+webhookCols+` FROM webhooks ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Webhook
	for rows.Next() {
		w, err := scanWebhook(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// DeleteWebhook removes a webhook with its deliveries.
func DeleteWebhook(ctx context.Context, q DBTX, id int64) error {
	return execOne(ctx, q, `DELETE FROM webhooks WHERE id=?`, id)
}

// Delivery is one event on its way to one webhook.
type Delivery struct {
	ID        int64
	WebhookID int64
	EventID   int64
	Type      string
	Body      string
	State     string // pending|ok|failed
	Attempts  int
	NextAt    int64
	CreatedAt int64
	Status    int
	Error     string
	TookMS    int64
}

const deliveryCols = `id, webhook_id, event_id, type, body, state, attempts, next_at, created_at, status, error, took_ms`

func scanDelivery(sc interface{ Scan(...any) error }) (*Delivery, error) {
	d := &Delivery{}
	err := sc.Scan(&d.ID, &d.WebhookID, &d.EventID, &d.Type, &d.Body, &d.State, &d.Attempts, &d.NextAt, &d.CreatedAt, &d.Status, &d.Error, &d.TookMS)
	return d, mapErr(err)
}

// DueDeliveries returns deliveries that should be tried now, oldest first.
func DueDeliveries(ctx context.Context, q DBTX, limit int) ([]*Delivery, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+deliveryCols+` FROM webhook_deliveries WHERE state='pending' AND next_at <= ? ORDER BY id LIMIT ?`, unix(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Delivery
	for rows.Next() {
		d, err := scanDelivery(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// NextDeliveryAt is when the next waiting delivery is due (0 = none waits).
func NextDeliveryAt(ctx context.Context, q DBTX) (int64, error) {
	var at sql.NullInt64
	err := q.QueryRowContext(ctx, `SELECT min(next_at) FROM webhook_deliveries WHERE state='pending'`).Scan(&at)
	return at.Int64, err
}

// GetDelivery loads one delivery.
func GetDelivery(ctx context.Context, q DBTX, id int64) (*Delivery, error) {
	return scanDelivery(q.QueryRowContext(ctx, `SELECT `+deliveryCols+` FROM webhook_deliveries WHERE id=?`, id))
}

// ListDeliveries returns the newest deliveries of a webhook.
func ListDeliveries(ctx context.Context, q DBTX, webhookID int64, limit int) ([]*Delivery, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+deliveryCols+` FROM webhook_deliveries WHERE webhook_id=? ORDER BY id DESC LIMIT ?`, webhookID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Delivery
	for rows.Next() {
		d, err := scanDelivery(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// SaveDelivery writes the outcome of an attempt.
func SaveDelivery(ctx context.Context, q DBTX, d *Delivery) error {
	return execOne(ctx, q, `UPDATE webhook_deliveries SET state=?, attempts=?, next_at=?, status=?, error=?, took_ms=? WHERE id=?`,
		d.State, d.Attempts, d.NextAt, d.Status, d.Error, d.TookMS, d.ID)
}

// AddDelivery queues a delivery of a body outside any event (a webhook test, a manual retry).
func AddDelivery(ctx context.Context, q DBTX, d *Delivery) error {
	d.CreatedAt, d.NextAt, d.State = unix(), unix(), "pending"
	res, err := q.ExecContext(ctx, `INSERT INTO webhook_deliveries (webhook_id, event_id, type, body, next_at, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		d.WebhookID, d.EventID, d.Type, d.Body, d.NextAt, d.CreatedAt)
	if err != nil {
		return err
	}
	d.ID, err = res.LastInsertId()
	return err
}

// WebhookResult records the last outcome on a webhook: ok resets the failure count, a delivery
// that gave up adds one.
func WebhookResult(ctx context.Context, q DBTX, id int64, status int, ok, gaveUp bool) error {
	switch {
	case ok:
		_, err := q.ExecContext(ctx, `UPDATE webhooks SET last_at=?, last_status=?, fails=0 WHERE id=?`, unix(), status, id)
		return err
	case gaveUp:
		_, err := q.ExecContext(ctx, `UPDATE webhooks SET last_at=?, last_status=?, fails=fails+1 WHERE id=?`, unix(), status, id)
		return err
	}
	_, err := q.ExecContext(ctx, `UPDATE webhooks SET last_at=?, last_status=? WHERE id=?`, unix(), status, id)
	return err
}
