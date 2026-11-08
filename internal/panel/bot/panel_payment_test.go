package bot

import (
	"encoding/json"
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

func TestPanelPaymentDeliveryWithoutNode(t *testing.T) {
	ctx := t.Context()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := service.New(st)
	if err := svc.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	p := &store.Provider{Name: "Hosting", URL: "https://hosting.example", LoginURL: "https://hosting.example/account?tab=renew&server=panel"}
	if err := store.CreateProvider(ctx, st.DB, p); err != nil {
		t.Fatal(err)
	}
	until, cost, cur, period, note := time.Now().Add(24*time.Hour).Unix(), 12.0, "€", "year", "private-note-never-send"
	if _, err := svc.SetPanelBilling(ctx, service.ActorCLI, service.PanelBillingInput{ProviderID: &p.ID, PaidUntil: &until, Cost: &cost, Currency: &cur, CostPeriod: &period, Note: &note}); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	sent := map[int64]int{}
	var fail atomic.Bool
	fail.Store(true)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var params struct {
			Chat   int64  `json:"chat_id"`
			Text   string `json:"text"`
			Markup struct {
				Keys [][]Button `json:"inline_keyboard"`
			} `json:"reply_markup"`
		}
		if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
			t.Error(err)
		}
		mu.Lock()
		sent[params.Chat]++
		mu.Unlock()
		if !strings.Contains(params.Text, "сервера панели") || !strings.Contains(params.Text, "12 € в год") || strings.Contains(params.Text, note) {
			t.Errorf("wrong panel reminder: %s", params.Text)
		}
		if len(params.Markup.Keys) != 1 || params.Markup.Keys[0][0].URL != p.LoginURL {
			t.Error("missing provider account button")
		}
		if fail.Load() && params.Chat == 2 {
			_, _ = w.Write([]byte(`{"ok":false,"error_code":503,"description":"offline"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
	}))
	defer api.Close()
	b := New(Config{Service: svc, API: api.URL})
	b.api = newClient(api.URL, token)
	b.onPanelPayment(ctx, until)
	check := func(want int64) {
		t.Helper()
		p, err := store.GetPanelBilling(ctx, st.DB)
		if err != nil || p.RemindedFor != want {
			t.Fatalf("delivery state: %+v / %v", p, err)
		}
	}
	check(0)
	if err := svc.SetSetting(ctx, service.ActorCLI, service.SettingBotAdmins, `[{"id":1},{"id":2}]`); err != nil {
		t.Fatal(err)
	}
	b.onPanelPayment(ctx, until)
	check(0)
	// A transport reconnect for the same bot keeps partial delivery progress.
	b.api = newClient(api.URL, token)
	b.onPanelPayment(ctx, until)
	fail.Store(false)
	b.onPanelPayment(ctx, until)
	check(until)
	b.onPanelPayment(ctx, until)
	mu.Lock()
	defer mu.Unlock()
	if sent[1] != 1 || sent[2] != 3 {
		t.Fatalf("partial delivery duplicated or skipped: %v", sent)
	}
	if nodes, err := svc.Nodes(ctx); err != nil || len(nodes) != 0 {
		t.Fatal("bot required a fake node")
	}
}
