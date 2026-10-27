package subscription

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/xray/xraytest"
)

func finalMasks(t *testing.T, e *env) []string {
	t.Helper()
	var configs []map[string]any
	if err := json.Unmarshal(e.get(e.path()+"/json", "x", "hw-1").Body.Bytes(), &configs); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, c := range configs {
		for _, o := range c["outbounds"].([]any) {
			ob := o.(map[string]any)
			if ob["tag"] != "proxy" {
				continue
			}
			b, _ := json.Marshal(ob["streamSettings"].(map[string]any)["finalmask"])
			out = append(out, string(b))
		}
		if os.Getenv("XRAY_BIN") != "" {
			raw, _ := json.Marshal(c)
			if err := xraytest.Binary(t).Test(context.Background(), raw); err != nil {
				t.Fatalf("xray rejects the finalmask: %v\n%s", err, raw)
			}
		}
	}
	return out
}

// The ClientHello fragment: off by default; on, every server in Xray JSON gets it; a connection
// point with its own finalmask keeps it; links never carry it.
func TestClientHelloFragment(t *testing.T) {
	e := newEnv(t)
	for _, fm := range finalMasks(t, e) {
		if fm != "null" {
			t.Fatalf("finalmask without the switch: %s", fm)
		}
	}

	r := service.DefaultClientRouting()
	r.Fragment.Enabled = true
	if err := e.svc.SetClientRouting(e.ctx, actor, &r); err != nil {
		t.Fatal(err)
	}
	want := `{"tcp":[{"settings":{"delay":"10-20","length":"10-20","packets":"tlshello"},"type":"fragment"}]}`
	got := finalMasks(t, e)
	if len(got) != 2 || got[0] != want || got[1] != want {
		t.Fatalf("finalmasks %v, want %s on both servers", got, want)
	}
	if links := e.get(e.path(), "v2rayNG/1.9", "hw-1").Body.String(); strings.Contains(links, "fragment") {
		t.Fatalf("links carry the fragment: %s", links)
	}

	// A connection point's own finalmask wins over the switch.
	nis, err := e.svc.UserInbounds(e.ctx, e.user.ID)
	if err != nil || len(nis) == 0 {
		t.Fatal(nis, err)
	}
	own := map[string]any{"tcp": []any{map[string]any{"type": "fragment", "settings": map[string]any{"packets": "1-3", "length": "5-9", "delay": "1"}}}}
	if err := e.svc.SetHostOverride(e.ctx, actor, nis[0].ID, map[string]any{"finalmask": own}); err != nil {
		t.Fatal(err)
	}
	ownJSON, _ := json.Marshal(own)
	got = finalMasks(t, e)
	if !strings.Contains(strings.Join(got, "\n"), string(ownJSON)) || !strings.Contains(strings.Join(got, "\n"), want) {
		t.Fatalf("finalmasks %v: want the connection point's own and the subscription's", got)
	}
}

func TestFragmentChecks(t *testing.T) {
	for _, f := range []service.ClientFragment{
		{Packets: "0-1"}, {Length: "0"}, {Length: "20-10"}, {Delay: "a"}, {Packets: "tls"},
	} {
		r := service.DefaultClientRouting()
		r.Fragment = f
		if err := service.CheckClientRouting(&r); err == nil {
			t.Errorf("accepted %+v", f)
		}
	}
	r := service.DefaultClientRouting()
	r.Fragment = service.ClientFragment{Enabled: true, Length: " 100-200 "}
	if err := service.CheckClientRouting(&r); err != nil || r.Fragment.Length != "100-200" || r.Fragment.Packets != "tlshello" || r.Fragment.Delay != "10-20" {
		t.Fatalf("%+v %v", r.Fragment, err)
	}

	// Hysteria2 is UDP: the TCP fragment is not for it, and a server with a raw link keeps it.
	hs := service.WithFragment([]service.Host{{Protocol: "hysteria"}, {Raw: "trojan://x"}, {Protocol: "vless"}}, r.Fragment)
	if hs[0].FinalMask != nil || hs[1].FinalMask != nil || hs[2].FinalMask == nil {
		t.Fatalf("%+v", hs)
	}

	for _, bad := range []any{
		"x", map[string]any{"foo": []any{}}, map[string]any{"tcp": map[string]any{}}, map[string]any{"tcp": []any{map[string]any{}}},
		// The snippet that goes around: Xray calls the pause delay, interval would be lost.
		map[string]any{"tcp": []any{map[string]any{"type": "fragment", "settings": map[string]any{"length": "10-20", "packets": "tlshello", "interval": "10-20"}}}},
	} {
		if err := service.CheckFinalMask(bad); err == nil {
			t.Errorf("accepted finalmask %v", bad)
		}
	}
}
