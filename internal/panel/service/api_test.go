package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/store"
)

func TestAPITokenLifecycle(t *testing.T) {
	f := newFixture(t)
	exp := f.now.Add(time.Hour)
	tok, secret, err := f.s.CreateAPIToken(f.ctx, ActorCLI, APITokenInput{Name: "billing", Scopes: []string{ScopeUsersRead, ScopeUsersRead, ScopeUsersWrite},
		CIDRs: []string{"10.0.0.5", "192.168.1.0/24"}, ExpiresAt: &exp})
	if err != nil {
		t.Fatal(err)
	}
	if len(tok.Scopes) != 2 || tok.CIDRs[0] != "10.0.0.5/32" {
		t.Fatalf("scopes/cidrs not normalized: %+v", tok)
	}
	if got, err := f.s.APITokens(f.ctx); err != nil || len(got) != 1 || got[0].Hash == secret || got[0].Prefix != secret[:8] {
		t.Fatalf("the list must hold the hash, not the secret: %+v %v", got, err)
	}
	if _, err := f.s.AuthenticateAPIToken(f.ctx, secret, "10.0.0.5"); err != nil {
		t.Fatalf("right token and address: %v", err)
	}
	if _, err := f.s.AuthenticateAPIToken(f.ctx, secret, "192.168.1.77"); err != nil {
		t.Fatalf("address in the network: %v", err)
	}
	if _, err := f.s.AuthenticateAPIToken(f.ctx, secret, "8.8.8.8"); !errors.Is(err, ErrTokenIP) {
		t.Fatalf("other address: %v", err)
	}
	if _, err := f.s.AuthenticateAPIToken(f.ctx, TokenPrefix+"nope", "10.0.0.5"); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("unknown token: %v", err)
	}
	if _, err := f.s.AuthenticateAPIToken(f.ctx, "Bearer x", "10.0.0.5"); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("no prefix: %v", err)
	}
	f.now = f.now.Add(2 * time.Hour)
	if _, err := f.s.AuthenticateAPIToken(f.ctx, secret, "10.0.0.5"); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("expired: %v", err)
	}
	f.now = f.now.Add(-2 * time.Hour)
	if err := f.s.RevokeAPIToken(f.ctx, ActorCLI, tok.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.AuthenticateAPIToken(f.ctx, secret, "10.0.0.5"); !errors.Is(err, ErrTokenRevoked) {
		t.Fatalf("revoked: %v", err)
	}
}

func TestAPITokenValidation(t *testing.T) {
	f := newFixture(t)
	past := f.now.Add(-time.Hour)
	for name, in := range map[string]APITokenInput{
		"no name":     {Scopes: []string{ScopeUsersRead}},
		"no scopes":   {Name: "x"},
		"bad scope":   {Name: "x", Scopes: []string{"root"}},
		"bad cidr":    {Name: "x", Scopes: []string{ScopeUsersRead}, CIDRs: []string{"nonsense"}},
		"bad rate":    {Name: "x", Scopes: []string{ScopeUsersRead}, RateLimit: -1},
		"in the past": {Name: "x", Scopes: []string{ScopeUsersRead}, ExpiresAt: &past},
	} {
		if _, _, err := f.s.CreateAPIToken(f.ctx, ActorCLI, in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
}

func TestEventsFromAudit(t *testing.T) {
	f := newFixture(t)
	u := must(f.s.CreateUser(f.ctx, Actor{Kind: "api", ID: "billing"}, CreateUserInput{Username: "alice"}))
	must(f.s.ExtendUser(f.ctx, ActorCLI, u.ID, 1, 0))
	must(f.s.SetUserEnabled(f.ctx, ActorCLI, u.ID, false))
	if err := f.s.DeleteUser(f.ctx, ActorCLI, u.ID); err != nil {
		t.Fatal(err)
	}
	evs := must(store.ListAPIEvents(f.ctx, f.s.st.DB, 0, nil, 50))
	var types []string
	for _, e := range evs {
		types = append(types, e.Type)
	}
	want := []string{"user.created", "user.extended", "user.disabled", "user.deleted"}
	if len(types) != len(want) {
		t.Fatalf("events %v, want %v", types, want)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("events %v, want %v", types, want)
		}
	}
	if evs[0].Actor != "api:billing" {
		t.Errorf("actor %q", evs[0].Actor)
	}
	var doc struct {
		ID     string `json:"id"`
		Entity struct {
			Username string `json:"username"`
		} `json:"entity"`
	}
	if err := json.Unmarshal(store.EventDoc(evs[3]), &doc); err != nil || doc.Entity.Username != "alice" || doc.ID == "" {
		t.Errorf("a deleted user must still be named in its event: %s", store.EventDoc(evs[3]))
	}
	if got := must(store.ListAPIEvents(f.ctx, f.s.st.DB, evs[1].ID, []string{"user.dis*"}, 50)); len(got) != 1 || got[0].Type != "user.disabled" {
		t.Errorf("filter by prefix after a cursor: %+v", got)
	}
}

func TestUserIdentity(t *testing.T) {
	f := newFixture(t)
	a := must(f.s.CreateUser(f.ctx, ActorCLI, CreateUserInput{Username: "a"}))
	b := must(f.s.CreateUser(f.ctx, ActorCLI, CreateUserInput{Username: "b"}))
	tg, ext := int64(42), "crm-7"
	if _, err := f.s.SetUserIdentity(f.ctx, ActorCLI, a.ID, UserIdentityInput{SetTelegramID: true, TelegramID: &tg, SetExternalID: true, ExternalID: &ext}); err != nil {
		t.Fatal(err)
	}
	_, err := f.s.SetUserIdentity(f.ctx, ActorCLI, b.ID, UserIdentityInput{SetTelegramID: true, TelegramID: &tg})
	var taken *IdentityTakenError
	if !errors.As(err, &taken) || taken.Field != "telegram_id" || !errors.Is(err, ErrConflict) {
		t.Fatalf("a Telegram account belongs to one user: %v", err)
	}
	if _, err := f.s.CreateUser(f.ctx, ActorCLI, CreateUserInput{Username: "c", ExternalID: &ext}); !errors.As(err, &taken) || taken.Field != "external_id" {
		t.Fatalf("create with a taken external id: %v", err)
	}
	got := must(f.s.Users(f.ctx, store.UserFilter{ExternalID: &ext}))
	if len(got) != 1 || got[0].ID != a.ID {
		t.Fatalf("lookup by external id: %+v", got)
	}
	if u, err := f.s.SetUserIdentity(f.ctx, ActorCLI, a.ID, UserIdentityInput{SetTelegramID: true}); err != nil || u.TelegramID != nil {
		t.Fatalf("clear: %v %+v", u, err)
	}
}

func TestWebhookDelivery(t *testing.T) {
	f := newFixture(t)
	var hits atomic.Int32
	var gotSig, gotTS string
	var gotBody []byte
	fail := atomic.Bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		gotSig, gotTS = r.Header.Get(HeaderSignature), r.Header.Get(HeaderTimestamp)
		gotBody, _ = io.ReadAll(r.Body)
		if fail.Load() {
			http.Error(w, "boom", http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	if _, err := f.s.CreateWebhook(f.ctx, ActorCLI, WebhookInput{Name: "h", URL: srv.URL, Enabled: true}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a local address needs allowing: %v", err)
	}
	w := must(f.s.CreateWebhook(f.ctx, ActorCLI, WebhookInput{Name: "h", URL: srv.URL, Enabled: true, AllowPrivate: true, Events: []string{"user.*"}}))
	if len(w.Secret) < 20 {
		t.Fatal("no secret")
	}
	d := must(f.s.TestWebhook(f.ctx, ActorCLI, w.ID))
	if d.State != "ok" || d.Status != 200 || hits.Load() != 1 {
		t.Fatalf("ping: %+v", d)
	}
	ts := must(parseInt(gotTS))
	if gotSig != SignWebhook(w.Secret, ts, gotBody) {
		t.Fatal("the signature does not match the body")
	}

	// An event is queued with the change that made it, only for webhooks that want it.
	must(f.s.CreateUser(f.ctx, ActorCLI, CreateUserInput{Username: "bob"}))
	must(f.s.CreateGroup(f.ctx, ActorCLI, "g", ""))
	due := must(store.DueDeliveries(f.ctx, f.s.st.DB, 10))
	if len(due) != 1 || due[0].Type != "user.created" {
		t.Fatalf("one delivery for user.created only: %+v", due)
	}

	// A failing receiver: the delivery waits for the next try.
	fail.Store(true)
	f.s.deliver(f.ctx, w, due[0], true)
	got := must(store.GetDelivery(f.ctx, f.s.st.DB, due[0].ID))
	if got.State != "pending" || got.Attempts != 1 || got.Status != 500 || got.NextAt <= f.now.Unix() {
		t.Fatalf("retry planned: %+v", got)
	}
	fail.Store(false)
	f.s.deliver(f.ctx, w, got, true)
	if got = must(store.GetDelivery(f.ctx, f.s.st.DB, due[0].ID)); got.State != "ok" || got.Attempts != 2 {
		t.Fatalf("delivered on the second try: %+v", got)
	}
	if hooks := must(f.s.Webhooks(f.ctx)); hooks[0].Fails != 0 {
		t.Fatalf("a success clears the failures: %+v", hooks[0])
	}
}

func TestWebhookSwitchesOff(t *testing.T) {
	f := newFixture(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no", 410) }))
	defer srv.Close()
	w := must(f.s.CreateWebhook(f.ctx, ActorCLI, WebhookInput{Name: "h", URL: srv.URL, Enabled: true, AllowPrivate: true}))
	for i := 0; i < webhookOffAfter; i++ {
		d := &store.Delivery{WebhookID: w.ID, Type: "user.created", Body: `{}`}
		must(d, store.AddDelivery(f.ctx, f.s.st.DB, d))
		d.Attempts = webhookMaxTries - 1 // the last try
		cur := must(f.s.Webhook(f.ctx, w.ID))
		f.s.deliver(f.ctx, cur, d, true)
	}
	got := must(f.s.Webhook(f.ctx, w.ID))
	if got.Enabled || got.OffReason == "" {
		t.Fatalf("a webhook nobody answers goes off: %+v", got)
	}
	if on := must(f.s.UpdateWebhook(f.ctx, ActorCLI, w.ID, WebhookInput{Name: "h", URL: srv.URL, Enabled: true, AllowPrivate: true})); on.Fails != 0 || on.OffReason != "" {
		t.Fatalf("switching on starts afresh: %+v", on)
	}
}

func TestWebhookInputs(t *testing.T) {
	f := newFixture(t)
	for _, in := range []WebhookInput{
		{Name: "", URL: "https://example.com/h"},
		{Name: "x", URL: "ftp://example.com/h"},
		{Name: "x", URL: "https://user:pw@example.com/h"},
		{Name: "x", URL: "https://example.com/h", Events: []string{"nonsense"}},
		{Name: "x", URL: "http://127.0.0.1:9/h"},
		{Name: "x", URL: "http://10.1.2.3/h"},
	} {
		if _, err := f.s.CreateWebhook(f.ctx, ActorCLI, in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: want ErrInvalid, got %v", in, err)
		}
	}
	if _, err := f.s.CreateWebhook(f.ctx, ActorCLI, WebhookInput{Name: "x", URL: "https://example.com/h", Events: []string{"user.*", "node.online"}}); err != nil {
		t.Fatal(err)
	}
}

func TestPollNodesTellsChanges(t *testing.T) {
	f := newFixture(t)
	n, _ := must2(f.s.CreateNode(f.ctx, ActorCLI, NodeInput{Name: "A", Country: "nl", Domain: "a.example.com"}))
	var up atomic.Bool
	f.s.NodeConnected = func(int64) bool { return up.Load() }
	f.s.pollNodes(f.ctx) // first look: not news
	up.Store(true)
	f.s.pollNodes(f.ctx)
	f.s.pollNodes(f.ctx) // held for two polls
	evs := must(store.ListAPIEvents(f.ctx, f.s.st.DB, 0, []string{"node.*"}, 50))
	var online int
	for _, e := range evs {
		if e.Type == "node.online" && e.EntityID == n.ID {
			online++
		}
	}
	if online != 1 {
		t.Fatalf("one node.online expected: %+v", evs)
	}
}

func parseInt(s string) (int64, error) {
	var n int64
	err := json.Unmarshal([]byte(s), &n)
	return n, err
}

func TestRunWebhooksDeliversEvents(t *testing.T) {
	f := newFixture(t)
	got := make(chan string, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- r.Header.Get(HeaderEvent)
	}))
	defer srv.Close()
	must(f.s.CreateWebhook(f.ctx, ActorCLI, WebhookInput{Name: "h", URL: srv.URL, Enabled: true, AllowPrivate: true, Events: []string{"user.created"}}))
	ctx, cancel := context.WithCancel(f.ctx)
	done := make(chan struct{})
	go func() { _ = f.s.RunWebhooks(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	must(f.s.CreateUser(f.ctx, ActorCLI, CreateUserInput{Username: "zed"}))
	select {
	case typ := <-got:
		if typ != "user.created" {
			t.Fatalf("event header %q", typ)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the loop did not deliver the event")
	}
}
