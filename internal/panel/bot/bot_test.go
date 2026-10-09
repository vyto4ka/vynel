package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
)

const token = "123456789:AAtesttokentesttokentesttokentest"

// fakeTG is a Telegram Bot API stand-in: it hands out queued updates and records what the
// bot sends.
type fakeTG struct {
	t      *testing.T
	mu     sync.Mutex
	queue  []Update
	nextID int64
	msgID  int64
	calls  []call
}

type call struct {
	Method string
	Params map[string]any
}

func (f *fakeTG) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 3 || parts[1] != "bot"+token {
		w.WriteHeader(401)
		_, _ = io.WriteString(w, `{"ok":false,"error_code":401,"description":"Unauthorized"}`)
		return
	}
	method := parts[2]
	params := map[string]any{}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		_ = r.ParseMultipartForm(32 << 20)
		for k, v := range r.MultipartForm.Value {
			params[k] = v[0]
		}
		for k, fs := range r.MultipartForm.File {
			params[k] = fs[0].Filename
			params[k+"_size"] = fs[0].Size
		}
	} else {
		_ = json.NewDecoder(r.Body).Decode(&params)
	}
	reply := func(v any) {
		b, _ := json.Marshal(map[string]any{"ok": true, "result": v})
		_, _ = w.Write(b)
	}
	f.mu.Lock()
	if method != "getUpdates" {
		f.calls = append(f.calls, call{method, params})
	}
	switch method {
	case "getMe":
		f.mu.Unlock()
		reply(User{ID: 1, IsBot: true, Username: "vynel_test_bot"})
	case "getUpdates":
		ups := f.queue
		f.queue = nil
		f.mu.Unlock()
		if len(ups) == 0 {
			time.Sleep(50 * time.Millisecond)
		}
		reply(ups)
	case "sendMessage":
		f.msgID++
		id := f.msgID
		f.mu.Unlock()
		reply(Message{MessageID: id})
	default:
		f.mu.Unlock()
		reply(true)
	}
}

func (f *fakeTG) push(u Update) {
	f.mu.Lock()
	f.nextID++
	u.UpdateID = f.nextID
	f.queue = append(f.queue, u)
	f.mu.Unlock()
}

func (f *fakeTG) text(from int64, text string) {
	f.push(Update{Message: &Message{MessageID: 1, From: &User{ID: from, FirstName: "Admin", Username: "admin"}, Chat: Chat{ID: from, Type: "private"}, Text: text}})
}

func (f *fakeTG) press(from int64, data string) {
	f.push(Update{CallbackQuery: &CallbackQuery{ID: "cb", From: User{ID: from}, Data: data,
		Message: &Message{MessageID: 77, Chat: Chat{ID: from, Type: "private"}}}})
}

// waitCall waits for a call matching fn that was made after the first `after` calls.
func (f *fakeTG) waitCall(t *testing.T, after int, what string, fn func(c call) bool) call {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		for _, c := range f.calls[min(after, len(f.calls)):] {
			if fn(c) {
				f.mu.Unlock()
				return c
			}
		}
		f.mu.Unlock()
		time.Sleep(20 * time.Millisecond)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	t.Fatalf("no %s; calls: %+v", what, f.calls[min(after, len(f.calls)):])
	return call{}
}

func (f *fakeTG) since(n int) []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]call(nil), f.calls[min(n, len(f.calls)):]...)
}

func (f *fakeTG) n() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func textHas(method, sub string) func(c call) bool {
	return func(c call) bool {
		s, _ := c.Params["text"].(string)
		return c.Method == method && strings.Contains(s, sub)
	}
}

func TestBot(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
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
	_ = svc.EnsureWebDefaults(ctx)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(svc.SetSetting(ctx, service.ActorCLI, service.SettingSubDomain, "nl.example.com"))
	must(svc.SetSetting(ctx, service.ActorCLI, service.SettingBotAlertDelay, "0"))
	must(svc.SetSetting(ctx, service.ActorCLI, service.SettingBotBackupTime, "off"))
	must(svc.SetSetting(ctx, service.ActorCLI, service.SettingBotSummaryTime, "off"))
	must(svc.SetBotToken(ctx, service.ActorCLI, token))
	node, err := svc.EnsureLocalNode(ctx, service.NodeInput{Name: "Нидерланды", Country: "nl", Domain: "nl.example.com"})
	must(err)

	fake := &fakeTG{t: t}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	var connected atomic.Bool
	connected.Store(true)
	b := New(Config{Service: svc, DataDir: t.TempDir(), Version: "test", API: srv.URL, Poll: time.Second, Tick: 50 * time.Millisecond,
		Connected: func(int64) bool { return connected.Load() }})
	done := make(chan struct{})
	go func() { _ = b.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	deadline := time.Now().Add(5 * time.Second)
	for !b.Status().Running && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if s := b.Status(); !s.Running || s.Username != "vynel_test_bot" {
		t.Fatalf("status %+v", s)
	}

	// Strangers get nothing, a wrong code binds nobody, the right code binds.
	n := fake.n()
	fake.text(99, "/users")
	fake.text(99, "/start 00000000")
	code, err := svc.NewBindCode(ctx, service.ActorCLI)
	must(err)
	fake.text(42, "/start "+code)
	fake.waitCall(t, n, "welcome", textHas("sendMessage", "вы администратор"))
	for _, c := range fake.since(n) {
		if id, _ := c.Params["chat_id"].(float64); id == 99 {
			t.Fatalf("answered a stranger: %+v", c)
		}
	}
	if !svc.IsBotAdmin(ctx, 42) || svc.IsBotAdmin(ctx, 99) {
		t.Fatal("binding went wrong")
	}

	// Users: create, extend.
	n = fake.n()
	fake.text(42, "/new vasya")
	fake.waitCall(t, n, "user card", textHas("sendMessage", "vasya"))
	u, err := svc.UserByUsername(ctx, "vasya")
	must(err)
	exp := *u.ExpireAt
	n = fake.n()
	fake.press(42, "ux:"+itoa(u.ID)+":1")
	fake.waitCall(t, n, "card edit", textHas("editMessageText", "vasya"))
	u, _ = svc.UserByUsername(ctx, "vasya")
	if *u.ExpireAt <= exp {
		t.Fatal("not extended")
	}
	n = fake.n()
	fake.text(42, "vas") // plain text = search; one match opens the card
	fake.waitCall(t, n, "search result", textHas("sendMessage", "👤 <b>vasya</b>"))

	// Web login link: one-time, in the URL fragment.
	n = fake.n()
	fake.text(42, "/login")
	c := fake.waitCall(t, n, "login link", textHas("sendMessage", "Вход в панель"))
	kb, _ := json.Marshal(c.Params["reply_markup"])
	link := string(kb)
	i := strings.Index(link, "#/magic/")
	if i < 0 {
		t.Fatalf("no magic link: %s", link)
	}
	tok := link[i+len("#/magic/"):]
	tok = tok[:strings.IndexAny(tok, `"\`)]
	if _, err := svc.UseLoginToken(ctx, tok); err != nil {
		t.Fatalf("login token: %v", err)
	}
	if _, err := svc.UseLoginToken(ctx, tok); err == nil {
		t.Fatal("login token worked twice")
	}

	// Node alerts: one message, edited while down, closed with a reply when back.
	n = fake.n()
	connected.Store(false)
	down := fake.waitCall(t, n, "down alert", textHas("sendMessage", "недоступна"))
	if id, _ := down.Params["chat_id"].(float64); id != 42 {
		t.Fatalf("alert to %v", down.Params["chat_id"])
	}
	time.Sleep(300 * time.Millisecond)
	sends := 0
	for _, c := range fake.since(n) {
		if textHas("sendMessage", "недоступна")(c) {
			sends++
		}
	}
	if sends != 1 {
		t.Fatalf("%d down messages instead of one", sends)
	}
	n = fake.n()
	connected.Store(true)
	fake.waitCall(t, n, "up edit", textHas("editMessageText", "снова в строю"))
	fake.waitCall(t, n, "up reply", textHas("sendMessage", "снова работает"))
	_ = node

	// Backup on demand.
	n = fake.n()
	fake.press(42, "bk")
	doc := fake.waitCall(t, n, "backup document", func(c call) bool { return c.Method == "sendDocument" })
	if name, _ := doc.Params["document"].(string); !strings.HasSuffix(name, ".tar.gz") {
		t.Fatalf("document %v", doc.Params)
	}

	// The nightly backup: due this minute -> sent once to every admin, not again the same day.
	n = fake.n()
	now := time.Now().In(svc.BotLocation(ctx))
	must(svc.SetSetting(ctx, service.ActorCLI, service.SettingBotBackupTime, now.Format("15:04")))
	night := fake.waitCall(t, n, "nightly backup", func(c call) bool { return c.Method == "sendDocument" })
	if cap, _ := night.Params["caption"].(string); !strings.Contains(cap, "Ночной бэкап") {
		t.Fatalf("caption %q", cap)
	}
	time.Sleep(300 * time.Millisecond)
	docs := 0
	for _, c := range fake.since(n) {
		if c.Method == "sendDocument" {
			docs++
		}
	}
	if docs != 1 {
		t.Fatalf("%d nightly backups instead of one", docs)
	}

	// The Mini App button in the admin's chat.
	fake.waitCall(t, 0, "menu button", func(c call) bool {
		return c.Method == "setChatMenuButton" && strings.Contains(fmt.Sprint(c.Params), "web_app")
	})

	// A sign-in is reported at once, with a button that ends that session.
	n = fake.n()
	ws, err := svc.StartWebSession(ctx, service.LoginPassword, "boss", "203.0.113.5", "Mozilla/5.0 (Windows NT 10.0) Chrome/120", time.Hour)
	must(err)
	c = fake.waitCall(t, n, "login notice", textHas("sendMessage", "Вход в панель"))
	if s, _ := c.Params["text"].(string); !strings.Contains(s, "Chrome · Windows") || !strings.Contains(s, "203.0.113.5") {
		t.Fatalf("login notice %q", s)
	}
	n = fake.n()
	fake.press(42, "sx:"+ws.ID)
	fake.waitCall(t, n, "sessions list", textHas("editMessageText", "Сессии веб-панели"))
	if svc.WebSessionActive(ctx, ws.ID, "") {
		t.Fatal("the session was not ended")
	}

	// A config the node cannot apply is reported once, and so is the recovery.
	n = fake.n()
	must(store.SetNodeRuntime(ctx, st.DB, node.ID, store.NodeRuntime{LastError: "xray: bad config"}))
	fake.waitCall(t, n, "apply error", textHas("sendMessage", "не применила конфигурацию"))
	n = fake.n()
	must(store.SetNodeRuntime(ctx, st.DB, node.ID, store.NodeRuntime{}))
	fake.waitCall(t, n, "apply ok", textHas("sendMessage", "применила конфигурацию"))

	// The panel moves: a fresh login button.
	n = fake.n()
	must(svc.SetSetting(ctx, service.ActorCLI, service.SettingWebPort, "47321"))
	fake.waitCall(t, n, "panel moved", textHas("sendMessage", ":47321"))

	// The firewall screen (off here: no table).
	t.Setenv("VYNEL_FIREWALL_CONF", t.TempDir()+"/firewall.json")
	n = fake.n()
	fake.press(42, "fw")
	fake.waitCall(t, n, "firewall screen", textHas("editMessageText", "Файрвол сервера панели"))

	// The daily summary on demand.
	n = fake.n()
	fake.text(42, "/summary")
	fake.waitCall(t, n, "summary", textHas("sendMessage", "Сводка за сутки"))
}

func itoa(i int64) string { b, _ := json.Marshal(i); return string(b) }
