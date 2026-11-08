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

func TestPaymentReminderDeliveryAndProviderButton(t *testing.T) {
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
	n, _, err := svc.CreateNode(ctx, service.ActorCLI, service.NodeInput{Name: "DE"})
	if err != nil {
		t.Fatal(err)
	}
	p := &store.Provider{Name: "Provider", URL: "https://provider.example", LoginURL: "https://provider.example/account?tab=renew&node=1"}
	if err := store.CreateProvider(ctx, st.DB, p); err != nil {
		t.Fatal(err)
	}
	until, cost := time.Now().Add(24*time.Hour).Unix(), 9.50
	if _, err := svc.SetNodeBilling(ctx, service.ActorCLI, n.ID, service.NodeBillingInput{ProviderID: &p.ID, PaidUntil: &until, Cost: &cost}); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	sent := map[int64]int{}
	var fail atomic.Bool
	fail.Store(true)
	var message string
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
		message = params.Text
		mu.Unlock()
		if len(params.Markup.Keys) != 1 || params.Markup.Keys[0][0].URL != p.LoginURL {
			t.Error("provider renewal button missing")
		}
		if fail.Load() && params.Chat == 2 {
			_, _ = w.Write([]byte(`{"ok":false,"error_code":503,"description":"try later"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
	}))
	defer api.Close()
	b := New(Config{Service: svc, API: api.URL})
	b.api = newClient(api.URL, token)
	defer b.api.http.CloseIdleConnections()
	notice := service.Notice{Kind: "billing", NodeID: n.ID, PaidUntil: until}
	b.onBilling(ctx, notice) // no recipients is not a delivery
	bill, _ := store.GetNodeBilling(ctx, st.DB, n.ID)
	if bill.RemindedFor != 0 {
		t.Fatal("no admins marked as delivered")
	}
	if err := svc.SetSetting(ctx, service.ActorCLI, service.SettingBotAdmins, `[{"id":1},{"id":2}]`); err != nil {
		t.Fatal(err)
	}
	b.onBilling(ctx, notice)
	bill, _ = store.GetNodeBilling(ctx, st.DB, n.ID)
	if bill.RemindedFor != 0 {
		t.Fatal("failed delivery acknowledged")
	}
	b.onBilling(ctx, notice) // retry only the recipient that failed
	fail.Store(false)
	b.onBilling(ctx, notice)
	b.onBilling(ctx, notice) // queued duplicate is ignored after acknowledgement
	bill, _ = store.GetNodeBilling(ctx, st.DB, n.ID)
	if bill.RemindedFor != until {
		t.Fatal("successful delivery not acknowledged")
	}
	mu.Lock()
	defer mu.Unlock()
	if sent[1] != 1 || sent[2] != 3 {
		t.Fatalf("partial retries: %v", sent)
	}
	if !strings.Contains(message, "9.50 ₽ в месяц") || !strings.Contains(message, "Provider") || !strings.Contains(message, "оплачена до") {
		t.Fatalf("missing billing details: %s", message)
	}
}
