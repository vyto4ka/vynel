package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/vyto4ka/vpn/internal/panel/store"
	"github.com/vyto4ka/vpn/internal/xrayconf"
)

type fixture struct {
	ctx context.Context
	s   *Service
	now time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	f := &fixture{ctx: ctx, s: New(st), now: time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)}
	f.s.SetClock(func() time.Time { return f.now })
	t.Cleanup(func() { store.Now = time.Now })
	if err := f.s.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	return f
}

// must and must2 panic on error; a panic fails the test with a stack trace.
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// setup creates two nodes with the Reality profile and the default group granting the whole profile.
func (f *fixture) setup(t *testing.T) (nl, de *store.Node, prof *store.Profile, group *store.Group) {
	t.Helper()
	nl, _ = must2(f.s.CreateNode(f.ctx, ActorCLI, NodeInput{Name: "Нидерланды", Country: "nl", Domain: "nl.example.com"}))
	de, _ = must2(f.s.CreateNode(f.ctx, ActorCLI, NodeInput{Name: "Германия", Country: "de", Domain: "de.example.com"}))
	prof = must(f.s.CreateProfile(f.ctx, ActorCLI, ProfileInput{Name: "Reality 443", TemplateID: "vless-reality-selfsteal"}))
	group = must(f.s.GroupByName(f.ctx, "Основная"))
	if err := f.s.GrantAccess(f.ctx, ActorCLI, group.ID, store.AccessProfile, prof.ID); err != nil {
		t.Fatal(err)
	}
	must(f.s.AttachProfile(f.ctx, ActorCLI, AttachInput{NodeID: nl.ID, ProfileID: prof.ID}))
	must(f.s.AttachProfile(f.ctx, ActorCLI, AttachInput{NodeID: de.ID, ProfileID: prof.ID}))
	return
}

func must2[A, B any](a A, b B, err error) (A, B) {
	if err != nil {
		panic(err)
	}
	return a, b
}

func TestCreateUserByNameOnly(t *testing.T) {
	f := newFixture(t)
	u := must(f.s.CreateUser(f.ctx, ActorCLI, CreateUserInput{Username: "vasya"}))
	if u.Status != store.StatusActive || u.ExpireAt == nil {
		t.Fatalf("status %s expire %v", u.Status, u.ExpireAt)
	}
	if want := f.now.AddDate(0, 3, 0).Unix(); *u.ExpireAt != want {
		t.Fatalf("default template is +3 months: got %v want %v", time.Unix(*u.ExpireAt, 0), time.Unix(want, 0))
	}
	groups := must(f.s.UserGroupIDs(f.ctx, u.ID))
	if len(groups) != 1 {
		t.Fatalf("default group not assigned: %v", groups)
	}
	if len(u.SubToken) < 40 || u.SubToken == u.UUID {
		t.Fatal("sub token must be long and differ from uuid")
	}
	if _, err := f.s.CreateUser(f.ctx, ActorCLI, CreateUserInput{Username: "vasya"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate username: %v", err)
	}
	if _, err := f.s.CreateUser(f.ctx, ActorCLI, CreateUserInput{Username: "bad name"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad username: %v", err)
	}
}

func TestStatusMachineAndExtend(t *testing.T) {
	f := newFixture(t)
	u := must(f.s.CreateUser(f.ctx, ActorCLI, CreateUserInput{Username: "u1"}))
	exp := *u.ExpireAt

	// Extending an active user counts from the current expiry, not from now.
	u = must(f.s.ExtendUser(f.ctx, ActorCLI, u.ID, 1, 0))
	if want := time.Unix(exp, 0).AddDate(0, 1, 0).Unix(); *u.ExpireAt != want {
		t.Fatalf("extend from expiry: got %v", time.Unix(*u.ExpireAt, 0))
	}

	// Time passes: the user expires.
	f.now = time.Unix(*u.ExpireAt, 0).Add(time.Hour)
	if n := must(f.s.RefreshStatuses(f.ctx)); n != 1 {
		t.Fatalf("refreshed %d", n)
	}
	if u = must(f.s.User(f.ctx, u.ID)); u.Status != store.StatusExpired {
		t.Fatalf("want expired, got %s", u.Status)
	}
	// Extending an expired user counts from now.
	u = must(f.s.ExtendUser(f.ctx, ActorCLI, u.ID, 0, 30))
	if u.Status != store.StatusActive || *u.ExpireAt != f.now.AddDate(0, 0, 30).Unix() {
		t.Fatalf("extend expired: %s %v", u.Status, time.Unix(*u.ExpireAt, 0))
	}

	limit := int64(100)
	u = must(f.s.SetUserTrafficLimit(f.ctx, ActorCLI, u.ID, &limit))
	u.TrafficUsedBytes = 150
	if computeStatus(u, f.now) != store.StatusLimited {
		t.Fatal("over limit must be limited")
	}

	u = must(f.s.SetUserEnabled(f.ctx, ActorCLI, u.ID, false))
	if u.Status != store.StatusDisabled {
		t.Fatalf("want disabled, got %s", u.Status)
	}
	u = must(f.s.SetUserEnabled(f.ctx, ActorCLI, u.ID, true))
	if u.Status != store.StatusActive {
		t.Fatalf("want active, got %s", u.Status)
	}
}

func TestAttachGivesEachNodeItsOwnInbound(t *testing.T) {
	f := newFixture(t)
	nl, de, prof, _ := f.setup(t)
	nlIn := must(f.s.NodeInbounds(f.ctx, nl.ID))[0]
	deIn := must(f.s.NodeInbounds(f.ctx, de.ID))[0]
	if nlIn.Tag != "VLESS_NL" || deIn.Tag != "VLESS_DE" {
		t.Fatalf("tags %s %s", nlIn.Tag, deIn.Tag)
	}
	if nlIn.Values["REALITY_PRIVATE_KEY"] == deIn.Values["REALITY_PRIVATE_KEY"] {
		t.Fatal("each node must get its own Reality key")
	}
	// A second inbound of the same profile on one node gets a suffix.
	second := must(f.s.AttachProfile(f.ctx, ActorCLI, AttachInput{NodeID: nl.ID, ProfileID: prof.ID, PortOverride: 8443}))
	if second.Tag != "VLESS_NL_2" {
		t.Fatalf("second tag %s", second.Tag)
	}
	// A node without a domain cannot render Reality self-steal.
	bare, _ := must2(f.s.CreateNode(f.ctx, ActorCLI, NodeInput{Name: "Без домена", Country: "fi"}))
	if _, err := f.s.AttachProfile(f.ctx, ActorCLI, AttachInput{NodeID: bare.ID, ProfileID: prof.ID}); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "NODE_DOMAIN") {
		t.Fatalf("want missing NODE_DOMAIN, got %v", err)
	}
	if n := must(f.s.NodeInbounds(f.ctx, bare.ID)); len(n) != 0 {
		t.Fatal("failed attach must not leave an inbound behind")
	}
}

func TestNodeCodes(t *testing.T) {
	f := newFixture(t)
	a, _ := must2(f.s.CreateNode(f.ctx, ActorCLI, NodeInput{Name: "A", Country: "NL"}))
	b, _ := must2(f.s.CreateNode(f.ctx, ActorCLI, NodeInput{Name: "B", Country: "NL"}))
	if a.Code != "NL" || b.Code != "NL2" {
		t.Fatalf("codes %s %s", a.Code, b.Code)
	}
}

func TestProfileUpdatePropagatesAndValidates(t *testing.T) {
	f := newFixture(t)
	nl, de, prof, _ := f.setup(t)
	before := must(f.s.DesiredStates(f.ctx))

	// Node-level override survives a profile change of the same field.
	nlIn := must(f.s.NodeInbounds(f.ctx, nl.ID))[0]
	must(f.s.UpdateNodeInbound(f.ctx, ActorCLI, nlIn.ID, NodeInboundInput{
		Override: map[string]any{"streamSettings": map[string]any{"sockopt": map[string]any{"tcpKeepAliveIdle": 90}}},
	}))
	must(f.s.UpdateProfile(f.ctx, ActorCLI, prof.ID, ProfileInput{
		Override: map[string]any{"streamSettings": map[string]any{"sockopt": map[string]any{"tcpKeepAliveIdle": 60}}},
	}))
	nlR := must(f.s.RenderNodeInbound(f.ctx, nlIn.ID))
	deR := must(f.s.RenderNodeInbound(f.ctx, must(f.s.NodeInbounds(f.ctx, de.ID))[0].ID))
	if keep(nlR.Inbound) != 90 || keep(deR.Inbound) != 60 {
		t.Fatalf("keepalive nl=%v de=%v", keep(nlR.Inbound), keep(deR.Inbound))
	}
	after := must(f.s.DesiredStates(f.ctx))
	if before[de.ID].Hash == after[de.ID].Hash {
		t.Fatal("profile change must change the desired state of its nodes")
	}

	// A profile change that breaks rendering is rejected as a whole.
	_, err := f.s.UpdateProfile(f.ctx, ActorCLI, prof.ID, ProfileInput{Values: map[string]any{"PORT": 70000}})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid port must be rejected, got %v", err)
	}
	if p := must(f.s.ProfileByName(f.ctx, "Reality 443")); p.Values["PORT"] != nil {
		t.Fatal("rejected change must not be saved")
	}
	// Node values cannot be set on the profile.
	if _, err := f.s.UpdateProfile(f.ctx, ActorCLI, prof.ID, ProfileInput{Values: map[string]any{"REALITY_SHORT_ID": "aa"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("scope check: %v", err)
	}
}

func keep(in map[string]any) any {
	return in["streamSettings"].(map[string]any)["sockopt"].(map[string]any)["tcpKeepAliveIdle"]
}

func TestAccessRulesAndDesiredUsers(t *testing.T) {
	f := newFixture(t)
	nl, de, prof, main := f.setup(t)
	alice := must(f.s.CreateUser(f.ctx, ActorCLI, CreateUserInput{Username: "alice"}))
	test := must(f.s.CreateGroup(f.ctx, ActorCLI, "Тест DE", ""))
	deIn := must(f.s.NodeInbounds(f.ctx, de.ID))[0]
	if err := f.s.GrantAccess(f.ctx, ActorCLI, test.ID, store.AccessNodeInbound, deIn.ID); err != nil {
		t.Fatal(err)
	}
	bob := must(f.s.CreateUser(f.ctx, ActorCLI, CreateUserInput{Username: "bob", GroupIDs: []int64{test.ID}}))

	ds := must(f.s.DesiredStates(f.ctx))
	if got := emails(ds[nl.ID].Users["VLESS_NL"]); got != UserEmail(alice.ID) {
		t.Fatalf("NL users: %s", got)
	}
	if got := emails(ds[de.ID].Users["VLESS_DE"]); got != UserEmail(alice.ID)+","+UserEmail(bob.ID) {
		t.Fatalf("DE users: %s", got)
	}

	// "Whole profile" rules include nodes added later.
	fi, _ := must2(f.s.CreateNode(f.ctx, ActorCLI, NodeInput{Name: "Финляндия", Country: "fi", Domain: "fi.example.com"}))
	must(f.s.AttachProfile(f.ctx, ActorCLI, AttachInput{NodeID: fi.ID, ProfileID: prof.ID}))
	ds = must(f.s.DesiredStates(f.ctx))
	if got := emails(ds[fi.ID].Users["VLESS_FI"]); got != UserEmail(alice.ID) {
		t.Fatalf("new node must inherit profile-wide access: %q", got)
	}

	// Disabled users leave every node; disabled nodes run no inbounds.
	must(f.s.SetUserEnabled(f.ctx, ActorCLI, alice.ID, false))
	must(f.s.UpdateNode(f.ctx, ActorCLI, fi.ID, NodeInput{Domain: "fi.example.com"}, false))
	ds = must(f.s.DesiredStates(f.ctx))
	if got := emails(ds[de.ID].Users["VLESS_DE"]); got != UserEmail(bob.ID) {
		t.Fatalf("after disable: %s", got)
	}
	if len(ds[fi.ID].Inbounds) != 0 {
		t.Fatal("disabled node must have no inbounds")
	}
	var cfg map[string]any
	if err := json.Unmarshal(ds[nl.ID].Config, &cfg); err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg["api"]; !ok {
		t.Fatal("config must carry the api section")
	}
	_ = main
}

func emails(cs []xrayconf.Client) string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Email)
	}
	return strings.Join(out, ",")
}

func TestInstallTokenSingleUseAndExpiry(t *testing.T) {
	f := newFixture(t)
	n, token := must2(f.s.CreateNode(f.ctx, ActorCLI, NodeInput{Name: "NL", Country: "nl"}))
	sign := func(*store.Node) (string, error) { return "serial-1", nil }
	if _, err := f.s.ConsumeInstallToken(f.ctx, "wrong", sign); !errors.Is(err, ErrInvalid) {
		t.Fatalf("wrong token: %v", err)
	}
	got := must(f.s.ConsumeInstallToken(f.ctx, token, sign))
	if got.ID != n.ID || must(f.s.Node(f.ctx, n.ID)).CertSerial != "serial-1" {
		t.Fatal("token must bind the certificate to the node")
	}
	if _, err := f.s.ConsumeInstallToken(f.ctx, token, sign); !errors.Is(err, ErrInvalid) {
		t.Fatalf("reuse must fail: %v", err)
	}
	token2 := must(f.s.ReissueInstallToken(f.ctx, ActorCLI, n.ID))
	if must(f.s.Node(f.ctx, n.ID)).CertSerial != "" {
		t.Fatal("reissue must revoke the certificate")
	}
	f.now = f.now.Add(InstallTokenTTL + time.Minute)
	if _, err := f.s.ConsumeInstallToken(f.ctx, token2, sign); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expired token: %v", err)
	}
}

func TestAuditAndOutbox(t *testing.T) {
	f := newFixture(t)
	calls := 0
	f.s.OnChange = func() { calls++ }
	must(f.s.CreateUser(f.ctx, Actor{Kind: "bot", ID: "123"}, CreateUserInput{Username: "x"}))
	audit := must(store.ListAudit(f.ctx, f.s.Store().DB, 1))
	if audit[0].Action != "user.create" || audit[0].Actor != "bot" || audit[0].ActorID != "123" {
		t.Fatalf("audit %+v", audit[0])
	}
	if id := must(store.LastEventID(f.ctx, f.s.Store().DB)); id == 0 {
		t.Fatal("no outbox event")
	}
	if calls != 1 {
		t.Fatalf("OnChange calls %d", calls)
	}
	// Failed changes leave no trace.
	_, _ = f.s.CreateUser(f.ctx, ActorCLI, CreateUserInput{Username: "x"})
	if a := must(store.ListAudit(f.ctx, f.s.Store().DB, 10)); len(a) != 1 {
		t.Fatalf("failed change audited: %d entries", len(a))
	}
}

func TestEgressAddress(t *testing.T) {
	f := newFixture(t)
	nl, _ := must2(f.s.CreateNode(f.ctx, ActorCLI, NodeInput{Name: "NL", Country: "nl", Domain: "nl.example.com"}))
	if err := f.s.SyncAddresses(f.ctx, nl.ID, []ReportedAddress{{IP: "203.0.113.10", Interface: "eth0", Primary: true}, {IP: "203.0.113.12", Interface: "eth0"}, {IP: "fe80::1"}, {IP: "127.0.0.1"}}); err != nil {
		t.Fatal(err)
	}
	addrs := must(f.s.Addresses(f.ctx, nl.ID))
	if len(addrs) != 2 {
		t.Fatalf("loopback and link-local addresses are skipped, got %d", len(addrs))
	}
	if !addrs[0].IsPrimary || addrs[0].IP != "203.0.113.10" {
		t.Fatalf("primary first: %+v", addrs[0])
	}
	vlessIP := addrs[1]
	prof := must(f.s.CreateProfile(f.ctx, ActorCLI, ProfileInput{Name: "R", TemplateID: "vless-reality-selfsteal"}))
	ni := must(f.s.AttachProfile(f.ctx, ActorCLI, AttachInput{NodeID: nl.ID, ProfileID: prof.ID, ListenAddressID: &vlessIP.ID}))
	r := must(f.s.RenderNodeInbound(f.ctx, ni.ID))
	if r.Inbound["listen"] != "203.0.113.12" || r.EgressIP != "203.0.113.12" {
		t.Fatalf("listen %v egress %v", r.Inbound["listen"], r.EgressIP)
	}
	// Addresses of another node are rejected.
	de, _ := must2(f.s.CreateNode(f.ctx, ActorCLI, NodeInput{Name: "DE", Country: "de", Domain: "de.example.com"}))
	if _, err := f.s.AttachProfile(f.ctx, ActorCLI, AttachInput{NodeID: de.ID, ProfileID: prof.ID, ListenAddressID: &vlessIP.ID}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("foreign address: %v", err)
	}
}
