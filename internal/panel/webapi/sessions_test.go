package webapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/service"
)

func TestSessionsAndPasswordLogin(t *testing.T) {
	a, svc, pw := newServer(t)
	login := `{"login":"boss","password":"` + pw + `"}`
	if w := a.do("POST", "/api/login", login, true); w.Code != 200 {
		t.Fatalf("login a: %d", w.Code)
	}
	b := &client{t: t, srv: a.srv, base: a.base}
	if w := b.do("POST", "/api/login", login, true); w.Code != 200 {
		t.Fatalf("login b: %d", w.Code)
	}
	// The bot hears about both sign-ins.
	for i := 0; i < 2; i++ {
		select {
		case n := <-svc.Notices():
			if n.Kind != "login" || n.Session.Method != service.LoginPassword {
				t.Fatalf("notice %+v", n)
			}
		default:
			t.Fatal("no login notice")
		}
	}
	var list struct {
		Sessions []struct {
			ID      string
			Current bool
		}
	}
	w := a.do("GET", "/api/sessions", "", false)
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Sessions) != 2 {
		t.Fatalf("sessions: %s", w.Body.String())
	}
	if w := a.do("POST", "/api/sessions/end-others", "{}", true); w.Code != 200 {
		t.Fatalf("end others: %d", w.Code)
	}
	if w := b.do("GET", "/api/users", "", false); w.Code != 401 {
		t.Fatalf("ended session still works: %d", w.Code)
	}
	if w := a.do("GET", "/api/users", "", false); w.Code != 200 {
		t.Fatalf("own session ended: %d", w.Code)
	}

	// The form can only be switched off with the bot in place.
	if w := a.do("PUT", "/api/account/password-login", `{"on":false}`, true); w.Code != 400 {
		t.Fatalf("password off without bot: %d", w.Code)
	}
	bindAdmin(t, svc, 4242)
	if w := a.do("PUT", "/api/account/password-login", `{"on":false}`, true); w.Code != 200 {
		t.Fatalf("password off: %d %s", w.Code, w.Body.String())
	}
	if w := b.do("POST", "/api/login", login, true); w.Code != 403 {
		t.Fatalf("password login while off: %d", w.Code)
	}
	if w := a.do("GET", "/api/users", "", false); w.Code != 200 {
		t.Fatalf("existing session must survive: %d", w.Code)
	}
	if w := a.do("POST", "/api/logout", "{}", true); w.Code != 200 {
		t.Fatalf("logout: %d", w.Code)
	}
}

const testBotToken = "123456789:AAtesttokentesttokentesttoken"

func bindAdmin(t *testing.T, svc *service.Service, id int64) {
	t.Helper()
	ctx := context.Background()
	if err := svc.SetBotToken(ctx, service.ActorCLI, testBotToken); err != nil {
		t.Fatal(err)
	}
	code, err := svc.NewBindCode(ctx, service.ActorCLI)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := svc.BindBotAdmin(ctx, code, service.BotAdmin{ID: id, Name: "boss"}); !ok || err != nil {
		t.Fatal("bind", ok, err)
	}
}

// initData signs Mini App data the way Telegram does.
func initData(token string, user int64, at time.Time) string {
	v := url.Values{"auth_date": {strconv.FormatInt(at.Unix(), 10)}, "query_id": {"AAH"}, "user": {`{"id":` + strconv.FormatInt(user, 10) + `,"first_name":"B"}`}}
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var lines []string
	for _, k := range keys {
		lines = append(lines, k+"="+v.Get(k))
	}
	secret := hmac.New(sha256.New, []byte("WebAppData"))
	secret.Write([]byte(token))
	m := hmac.New(sha256.New, secret.Sum(nil))
	m.Write([]byte(strings.Join(lines, "\n")))
	v.Set("hash", hex.EncodeToString(m.Sum(nil)))
	return v.Encode()
}

func TestTelegramMiniAppLogin(t *testing.T) {
	c, svc, _ := newServer(t)
	bindAdmin(t, svc, 4242)
	body := func(s string) string { b, _ := json.Marshal(map[string]string{"initData": s}); return string(b) }
	for name, data := range map[string]string{
		"stranger": initData(testBotToken, 777, time.Now()),
		"old":      initData(testBotToken, 4242, time.Now().Add(-time.Hour)),
		"forged":   initData("123456789:AAanothertokenanothertokenxx", 4242, time.Now()),
	} {
		if w := c.do("POST", "/api/login/telegram", body(data), true); w.Code != 401 {
			t.Fatalf("%s: %d", name, w.Code)
		}
	}
	if w := c.do("POST", "/api/login/telegram", body(initData(testBotToken, 4242, time.Now())), true); w.Code != 200 {
		t.Fatalf("admin: %d %s", w.Code, w.Body.String())
	}
	if w := c.do("GET", "/api/users", "", false); w.Code != 200 {
		t.Fatalf("after mini app login: %d", w.Code)
	}
}
