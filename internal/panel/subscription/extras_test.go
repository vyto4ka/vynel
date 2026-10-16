package subscription

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
)

// cascadeEnv adds a second node (DE) with the Reality profile and a cascade NL → DE.
func cascadeEnv(t *testing.T) (*env, *store.Cascade) {
	t.Helper()
	e := newEnv(t)
	de, _, err := e.svc.CreateNode(e.ctx, actor, service.NodeInput{Name: "Германия", Country: "de", Domain: "de.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	prof, _ := e.svc.ProfileByName(e.ctx, "Reality")
	exit, err := e.svc.AttachProfile(e.ctx, actor, service.AttachInput{NodeID: de.ID, ProfileID: prof.ID})
	if err != nil {
		t.Fatal(err)
	}
	entry, err := e.svc.NodeInboundByTag(e.ctx, "VLESS_NL")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CreateCascade(e.ctx, actor, service.CascadeInput{Name: "x", EntryInboundID: entry.ID, ExitInboundID: entry.ID, Enabled: true}); err == nil {
		t.Fatal("a cascade into the same node was accepted")
	}
	c, err := e.svc.CreateCascade(e.ctx, actor, service.CascadeInput{Name: "NL-DE", EntryInboundID: entry.ID, ExitInboundID: exit.ID, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return e, c
}

func clientsOf(ds *service.DesiredState, tag string) []string {
	var out []string
	for _, c := range ds.Users[tag] {
		out = append(out, c.Email+"="+c.ID)
	}
	return out
}

// A user given a cascade gets a server named "entry → exit" with an own secret; the entry node
// knows that secret and sends its traffic to the exit node, which knows the entry node.
func TestCascade(t *testing.T) {
	e, c := cascadeEnv(t)
	links0 := links(t, e.get(e.path(), "v2rayNG/1.9", "hw-1"))

	if _, err := e.svc.PutGrant(e.ctx, actor, service.GrantInput{UserID: e.user.ID, Kind: store.GrantCascade, RefID: c.ID}); err != nil {
		t.Fatal(err)
	}
	ls := links(t, e.get(e.path(), "v2rayNG/1.9", "hw-1"))
	if len(ls) != len(links0)+1 {
		t.Fatalf("links %v", ls)
	}
	secret := service.CascadeUUID(e.user.UUID, c.ID)
	last := ls[len(ls)-1]
	if !strings.HasPrefix(last, "vless://"+secret+"@nl.example.com:443") || !strings.HasSuffix(last, "#"+urlEsc("🇳🇱 Нидерланды → 🇩🇪 Германия")) {
		t.Fatalf("cascade link %s", last)
	}

	states, err := e.svc.DesiredStates(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	nl, _ := e.svc.NodeByCode(e.ctx, "NL")
	de, _ := e.svc.NodeByCode(e.ctx, "DE")
	email := service.CascadeEmail(e.user.ID, c.ID)
	if !slices.Contains(clientsOf(states[nl.ID], "VLESS_NL"), email+"="+secret) {
		t.Fatalf("entry clients %v", clientsOf(states[nl.ID], "VLESS_NL"))
	}
	if !slices.Contains(clientsOf(states[de.ID], "VLESS_DE"), "c"+itoa(c.ID)+"="+c.ServiceUUID) {
		t.Fatalf("exit clients %v", clientsOf(states[de.ID], "VLESS_DE"))
	}
	var cfg struct {
		Outbounds []map[string]any `json:"outbounds"`
		Routing   struct {
			Rules []map[string]any `json:"rules"`
		} `json:"routing"`
	}
	if err := json.Unmarshal(states[nl.ID].Config, &cfg); err != nil {
		t.Fatal(err)
	}
	tag := service.CascadeTag(c.ID)
	if r := cfg.Routing.Rules[0]; r["outboundTag"] != tag {
		t.Fatalf("the cascade rule is not first: %v", cfg.Routing.Rules)
	}
	found := false
	for _, o := range cfg.Outbounds {
		if o["tag"] == tag {
			found = true
			raw, _ := json.Marshal(o)
			if !strings.Contains(string(raw), `"address":"de.example.com"`) || !strings.Contains(string(raw), c.ServiceUUID) {
				t.Fatalf("cascade outbound %s", raw)
			}
		}
	}
	if !found {
		t.Fatalf("no cascade outbound: %v", cfg.Outbounds)
	}

	// Disabling the cascade takes it away everywhere.
	if _, err := e.svc.UpdateCascade(e.ctx, actor, c.ID, service.CascadeInput{Name: c.Name, EntryInboundID: c.EntryInboundID, ExitInboundID: c.ExitInboundID}); err != nil {
		t.Fatal(err)
	}
	if ls := links(t, e.get(e.path(), "v2rayNG/1.9", "hw-1")); len(ls) != len(links0) {
		t.Fatalf("disabled cascade still listed: %v", ls)
	}
	states, _ = e.svc.DesiredStates(e.ctx)
	for _, cl := range clientsOf(states[nl.ID], "VLESS_NL") {
		if strings.HasPrefix(cl, email) {
			t.Fatal("disabled cascade still on the entry node")
		}
	}
}

func urlEsc(s string) string { return url.PathEscape(s) }

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// A grant through a group and the user's own grant count once; the user's own end date ends
// only the own grant, the group's stays.
func TestGrantsDedupAndExpiry(t *testing.T) {
	e, c := cascadeEnv(t)
	main, _ := e.svc.GroupByName(e.ctx, "Основная")
	past := time.Now().Add(-time.Hour)
	if _, err := e.svc.PutGrant(e.ctx, actor, service.GrantInput{GroupID: main.ID, Kind: store.GrantCascade, RefID: c.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.PutGrant(e.ctx, actor, service.GrantInput{UserID: e.user.ID, Kind: store.GrantCascade, RefID: c.ID, ExpireAt: &past}); err != nil {
		t.Fatal(err)
	}
	gs, err := e.svc.UserGrants(e.ctx, e.user.ID)
	if err != nil || len(gs) != 1 || !gs[0].Active || gs[0].ExpireAt != nil || gs[0].GrantID == 0 || len(gs[0].Groups) != 1 {
		t.Fatalf("grants %+v %v", gs, err)
	}
	// Giving it again changes the date of the same grant.
	again := past.Add(-time.Hour)
	g2, err := e.svc.PutGrant(e.ctx, actor, service.GrantInput{UserID: e.user.ID, Kind: store.GrantCascade, RefID: c.ID, ExpireAt: &again})
	if err != nil || g2.ID != gs[0].GrantID {
		t.Fatalf("regrant: %+v %v (was %d)", g2, err, gs[0].GrantID)
	}
	if all, _ := e.svc.Grants(e.ctx); len(all) != 2 {
		t.Fatalf("grants after regrant: %d", len(all))
	}
	n := 0
	for _, l := range links(t, e.get(e.path(), "v2rayNG/1.9", "hw-1")) {
		if strings.Contains(l, service.CascadeUUID(e.user.UUID, c.ID)) {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("cascade listed %d times", n)
	}
	// Without the group's grant the user's ended one gives nothing.
	all, _ := e.svc.Grants(e.ctx)
	for _, g := range all {
		if g.GroupID != nil {
			_ = e.svc.DeleteGrant(e.ctx, actor, g.ID)
		}
	}
	gs, _ = e.svc.UserGrants(e.ctx, e.user.ID)
	if len(gs) != 1 || gs[0].Active {
		t.Fatalf("ended grant active: %+v", gs)
	}
	for _, l := range links(t, e.get(e.path(), "v2rayNG/1.9", "hw-1")) {
		if strings.Contains(l, service.CascadeUUID(e.user.UUID, c.ID)) {
			t.Fatal("ended cascade listed")
		}
	}
}

const extRealityLink = "vless://11111111-2222-4333-8444-555555555555@friend.example.org:443?type=tcp&security=reality&sni=www.example.org&fp=chrome&pbk=PUBKEY&sid=ab&flow=xtls-rprx-vision#Friend%20NL"

// Another subscription merged in: fetched over HTTP (base64 links), its VLESS servers go to every
// format, other links only to the links format; listed once even when given twice.
func TestExternalSubscription(t *testing.T) {
	e := newEnv(t)
	var gotUA, gotDepth string
	friend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA, gotDepth = r.UserAgent(), r.Header.Get(service.DepthHeader)
		body := extRealityLink + "\nvmess://eyJhZGQiOiJ4In0=#VMess\n"
		_, _ = w.Write([]byte(base64.StdEncoding.EncodeToString([]byte(body))))
	}))
	defer friend.Close()
	ext, err := e.svc.CreateExternalSub(e.ctx, actor, service.ExternalSubInput{Name: "friend", URL: friend.URL + "/sub/abc", Enabled: true})
	if err != nil || ext.Error != "" || !strings.Contains(ext.Links, "vmess://") {
		t.Fatalf("external %+v %v", ext, err)
	}
	if gotDepth != "1" || !strings.Contains(gotUA, "v2rayNG") {
		t.Fatalf("request: UA %q depth %q", gotUA, gotDepth)
	}
	main, _ := e.svc.GroupByName(e.ctx, "Основная")
	future := time.Now().Add(24 * time.Hour)
	for _, gi := range []service.GrantInput{
		{GroupID: main.ID, Kind: store.GrantExternal, RefID: ext.ID},
		{UserID: e.user.ID, Kind: store.GrantExternal, RefID: ext.ID, ExpireAt: &future},
	} {
		if _, err := e.svc.PutGrant(e.ctx, actor, gi); err != nil {
			t.Fatal(err)
		}
	}
	ls := links(t, e.get(e.path(), "v2rayNG/1.9", "hw-1"))
	if strings.Count(strings.Join(ls, "\n"), "friend.example.org") != 1 || !slices.ContainsFunc(ls, func(l string) bool { return strings.HasPrefix(l, "vmess://") }) {
		t.Fatalf("links %v", ls)
	}
	var doc struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	_ = yaml.Unmarshal(e.get(e.path(), "clash-verge/v2.0 mihomo", "hw-1").Body.Bytes(), &doc)
	var friendProxy map[string]any
	for _, p := range doc.Proxies {
		if p["server"] == "friend.example.org" {
			friendProxy = p
		}
		if p["type"] == "vmess" {
			t.Fatal("a raw link went to Mihomo")
		}
	}
	if friendProxy == nil || friendProxy["uuid"] != "11111111-2222-4333-8444-555555555555" {
		t.Fatalf("mihomo proxies %v", doc.Proxies)
	}
	// A panel merging this subscription gets our servers only.
	if ls := links(t, e.get(e.path(), "v2rayNG/1.9", "hw-1", service.DepthHeader, "1")); strings.Contains(strings.Join(ls, " "), "friend.example.org") {
		t.Fatalf("merged servers echoed to another panel: %v", ls)
	}

	// The friend's site breaks: the last good copy stays, the error is shown.
	friend.Close()
	if err := e.svc.RefreshExternalSub(e.ctx, ext.ID); err == nil {
		t.Fatal("no error from a closed site")
	}
	es, _ := e.svc.ExternalSubs(e.ctx)
	if es[0].Error == "" || !strings.Contains(es[0].Links, "friend.example.org") {
		t.Fatalf("after a failure: %+v", es[0])
	}
}

// A link of this very panel is read directly: another user's servers come along.
func TestExternalOwnSubscription(t *testing.T) {
	e := newEnv(t)
	bob, err := e.svc.CreateUser(e.ctx, actor, service.CreateUserInput{Username: "bob"})
	if err != nil {
		t.Fatal(err)
	}
	link, _ := e.svc.SubscriptionURL(e.ctx, bob)
	ext, err := e.svc.CreateExternalSub(e.ctx, actor, service.ExternalSubInput{Name: "bob", URL: link, Enabled: true})
	if err != nil || ext.Error != "" || !strings.Contains(ext.Links, bob.UUID) {
		t.Fatalf("own link: %+v %v", ext, err)
	}
	if _, err := e.svc.PutGrant(e.ctx, actor, service.GrantInput{UserID: e.user.ID, Kind: store.GrantExternal, RefID: ext.ID}); err != nil {
		t.Fatal(err)
	}
	ls := links(t, e.get(e.path(), "v2rayNG/1.9", "hw-1"))
	if strings.Count(strings.Join(ls, "\n"), bob.UUID) == 0 || strings.Count(strings.Join(ls, "\n"), e.user.UUID) == 0 {
		t.Fatalf("links %v", ls)
	}
}

func TestParseLinks(t *testing.T) {
	hs := service.ParseLinks(extRealityLink + "\nhy2://pass@hy.example.org:443/?sni=hy.example.org#HY\n" +
		"vless://u@ws.example.org:443?type=ws&security=tls#WS\nhysteria2://p@x.example.org:443/?insecure=1#Insecure\n")
	if len(hs) != 4 || hs[0].Protocol != "vless" || hs[0].PublicKey != "PUBKEY" || hs[0].Flow != "xtls-rprx-vision" || hs[0].Remark != "Friend NL" ||
		hs[1].Protocol != "hysteria" || hs[1].UUID != "pass" || hs[2].Raw == "" || hs[3].Raw == "" {
		t.Fatalf("%+v", hs)
	}
}
