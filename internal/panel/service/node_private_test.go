package service

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/vyto4ka/vynel/internal/panel/store"
)

func TestNodePrivateFieldsStayOutOfClientData(t *testing.T) {
	f := newFixture(t)
	n, _, _, _ := f.setup(t)
	u := must(f.s.CreateUser(f.ctx, ActorCLI, CreateUserInput{Username: "private-fields"}))
	before := must(f.s.DesiredState(f.ctx, n.ID))
	hostsBefore := must(f.s.UserHosts(f.ctx, u.ID))
	note, release := "Провайдер: internal-rack-729\nОплата 14 числа", "26.9.30"
	n = must(f.s.UpdateNode(f.ctx, ActorCLI, n.ID, NodeInput{Domain: n.Domain, Note: &note, XrayRelease: &release}, true))
	if n.Note != note || n.XrayRelease != "v26.9.30" {
		t.Fatalf("saved fields: %+v", n)
	}
	// Unrelated updates from old clients and runtime facts must not clear either field.
	n = must(f.s.UpdateNode(f.ctx, ActorCLI, n.ID, NodeInput{Name: n.Name, Domain: n.Domain}, false))
	n = must(f.s.UpdateNode(f.ctx, ActorCLI, n.ID, NodeInput{Domain: n.Domain}, true))
	if err := store.SetNodeRuntime(f.ctx, f.s.st.DB, n.ID, store.NodeRuntime{XrayVersion: "26.10.31"}); err != nil {
		t.Fatal(err)
	}
	n = must(f.s.Node(f.ctx, n.ID))
	if n.Note != note || n.XrayRelease != "v26.9.30" || n.XrayVersion != "26.10.31" {
		t.Fatalf("lost fields: %+v", n)
	}
	after := must(f.s.DesiredState(f.ctx, n.ID))
	hostsAfter := must(f.s.UserHosts(f.ctx, u.ID))
	if before.Hash != after.Hash {
		t.Fatal("private fields changed the node's config")
	}
	b, _ := json.Marshal(hostsBefore)
	a, _ := json.Marshal(hostsAfter)
	if string(a) != string(b) {
		t.Fatal("private fields changed subscription hosts")
	}
	events := must(store.ListAPIEvents(f.ctx, f.s.st.DB, 0, []string{"node.*"}, 100))
	for _, value := range []any{after, hostsAfter, events} {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "internal-rack-729") || strings.Contains(string(data), "v26.9.30") {
			t.Fatalf("private fields leaked: %s", data)
		}
	}
	empty := ""
	n = must(f.s.UpdateNode(f.ctx, ActorCLI, n.ID, NodeInput{Domain: n.Domain, Note: &empty, XrayRelease: &empty}, true))
	if n.Note != "" || n.XrayRelease != "" {
		t.Fatalf("fields not cleared: %+v", n)
	}
}

func TestCreateNodePrivateFieldsValidation(t *testing.T) {
	f := newFixture(t)
	note, release := "  Мой сервер  ", "v26.10.31"
	n, _, err := f.s.CreateNode(f.ctx, ActorCLI, NodeInput{Note: &note, XrayRelease: &release})
	if err != nil {
		t.Fatal(err)
	}
	n = must(f.s.Node(f.ctx, n.ID))
	if n.Note != "Мой сервер" || n.XrayRelease != release {
		t.Fatalf("create lost fields: %+v", n)
	}
	note = strings.Repeat("я", 4001)
	if _, _, err := f.s.CreateNode(f.ctx, ActorCLI, NodeInput{Note: &note}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("long note: %v", err)
	}
	release = "https://example.com/xray"
	if _, err := f.s.UpdateNode(f.ctx, ActorCLI, n.ID, NodeInput{XrayRelease: &release}, true); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unsafe release: %v", err)
	}
}
