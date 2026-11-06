package bot

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
	"github.com/vyto4ka/vynel/internal/telegramrelay"
)

func TestNodeRouteNeverUsesDirectAPIOrEnvironmentProxy(t *testing.T) {
	var direct, routed atomic.Int64
	api := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { direct.Add(1) }))
	defer api.Close()
	t.Setenv("HTTPS_PROXY", api.URL)
	t.Setenv("NO_PROXY", "*")
	b := New(Config{API: api.URL, DialTelegram: func(_ context.Context, id int64) (net.Conn, error) {
		if id != 42 {
			t.Errorf("wrong node %d", id)
		}
		routed.Add(1)
		return nil, errors.New("selected node is offline")
	}})
	c, err := b.makeClient(token, 42)
	if err != nil {
		t.Fatal(err)
	}
	defer c.http.CloseIdleConnections()
	_, err = c.getMe(t.Context())
	if err == nil || !strings.Contains(err.Error(), "offline") || strings.Contains(err.Error(), token) {
		t.Fatalf("unsafe connection error: %v", err)
	}
	if direct.Load() != 0 || routed.Load() == 0 {
		t.Fatalf("route bypassed: direct=%d routed=%d", direct.Load(), routed.Load())
	}
}

func TestTokenlessNetworkProbeAndErrorKinds(t *testing.T) {
	st, err := store.Open(t.Context(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := service.New(st)
	var calls atomic.Int64
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodHead || r.URL.Path != "/" {
			t.Errorf("unexpected tokenless probe: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Location", "https://must-not-follow.example/")
		w.WriteHeader(http.StatusFound)
	}))
	b := New(Config{Service: svc, API: api.URL})
	if err := b.CheckNetwork(t.Context(), 0); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || svc.BotToken(t.Context()) != "" {
		t.Fatal("probe sent a token or followed a redirect")
	}
	api.Close()
	if err := b.CheckNetwork(t.Context(), 0); err == nil || ConnectionIssue(err) != "network" {
		t.Fatalf("network failure: %v", err)
	}
	for _, c := range []struct {
		err  error
		want string
	}{{&apiError{Code: 401}, "token"}, {&apiError{Code: 409}, "conflict"}, {&apiError{Code: 503}, "telegram"}, {errors.New("bad route"), "route"}, {context.DeadlineExceeded, "network"}} {
		if got := ConnectionIssue(c.err); got != c.want {
			t.Fatalf("issue %v: %s", c.err, got)
		}
	}
	// The tokenless probe must use the selected node too, without direct fallback.
	n, _, err := svc.CreateNode(t.Context(), service.ActorCLI, service.NodeInput{Name: "DE"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetNodeFacts(t.Context(), st.DB, n.ID, store.NodeFacts{Capabilities: []string{telegramrelay.Capability}}); err != nil {
		t.Fatal(err)
	}
	var routed atomic.Int64
	b.cfg.DialTelegram = func(context.Context, int64) (net.Conn, error) { routed.Add(1); return nil, errors.New("node offline") }
	if err := b.CheckNetwork(t.Context(), n.ID); err == nil || ConnectionIssue(err) != "network" || routed.Load() == 0 {
		t.Fatalf("node probe: %v / %d", err, routed.Load())
	}
}

func TestRouteSwitchAndConnectionCheck(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := service.New(st)
	if err := svc.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetBotToken(ctx, service.ActorCLI, token); err != nil {
		t.Fatal(err)
	}
	n, _, err := svc.CreateNode(ctx, service.ActorCLI, service.NodeInput{Name: "overseas", Country: "DE"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetNodeFacts(ctx, st.DB, n.ID, store.NodeFacts{Capabilities: []string{telegramrelay.Capability}}); err != nil {
		t.Fatal(err)
	}
	fake := &fakeTG{t: t}
	api := httptest.NewServer(fake)
	defer api.Close()
	var routeCalls atomic.Int64
	b := New(Config{Service: svc, API: api.URL, ReloadEvery: 20 * time.Millisecond, Poll: time.Second, Tick: 50 * time.Millisecond,
		DialTelegram: func(context.Context, int64) (net.Conn, error) {
			routeCalls.Add(1)
			return nil, errors.New("route offline")
		}})
	done := make(chan struct{})
	go func() { _ = b.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	wait := func(what string, fn func() bool) {
		t.Helper()
		until := time.Now().Add(3 * time.Second)
		for time.Now().Before(until) {
			if fn() {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("%s: %+v", what, b.Status())
	}
	wait("direct bot did not start", func() bool { return b.Status().Running })
	fake.text(99, "/help")
	wait("update offset not recorded", func() bool { b.mu.Lock(); defer b.mu.Unlock(); return b.offset > 0 })
	if err := svc.SetBotNode(ctx, service.ActorCLI, n.ID); err != nil {
		t.Fatal(err)
	}
	wait("bot did not switch to node", func() bool {
		s := b.Status()
		return routeCalls.Load() > 0 && !s.Running && strings.Contains(s.Error, "offline")
	})
	if err := svc.SetBotNode(ctx, service.ActorCLI, 0); err != nil {
		t.Fatal(err)
	}
	wait("bot did not return to direct", func() bool { return b.Status().Running })
	b.mu.Lock()
	offset := b.offset
	b.mu.Unlock()
	if offset == 0 {
		t.Fatal("route change lost the Telegram update offset")
	}
	before := fake.n()
	username, err := b.CheckConnection(ctx, 0)
	if err != nil || username != "vynel_test_bot" {
		t.Fatalf("check: %q %v", username, err)
	}
	for _, c := range fake.since(before) {
		if c.Method == "sendMessage" {
			t.Fatal("connection check sent a message")
		}
	}
	if id, _ := svc.BotNodeID(ctx); id != 0 {
		t.Fatal("check changed the saved route")
	}
	// A deleted route stays an error; it never quietly becomes direct.
	if err := svc.SetBotNode(ctx, service.ActorCLI, n.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteNode(ctx, service.ActorCLI, n.ID); err != nil {
		t.Fatal(err)
	}
	wait("deleted route was not reported", func() bool { return !b.Status().Running && strings.Contains(b.Status().Error, "не найдена") })
}

func TestPollingFailureClearsHealthyStatus(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := service.New(st)
	if err := svc.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetBotToken(ctx, service.ActorCLI, token); err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/getMe") {
			_, _ = w.Write([]byte(`{"ok":true,"result":{"username":"poll_test_bot"}}`))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/getUpdates") {
			_, _ = w.Write([]byte(`{"ok":false,"error_code":503,"description":"upstream unavailable"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer api.Close()
	b := New(Config{Service: svc, API: api.URL})
	done := make(chan struct{})
	go func() { _ = b.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	until := time.Now().Add(2 * time.Second)
	for time.Now().Before(until) {
		if s := b.Status(); !s.Running && strings.Contains(s.Error, "upstream unavailable") && s.Username == "poll_test_bot" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("stale healthy status after a polling failure: %+v", b.Status())
}
