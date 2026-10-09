package subscription

import (
	"net/http/httptest"
	"testing"

	"github.com/vyto4ka/vynel/internal/panel/service"
)

func TestDetectRules(t *testing.T) {
	def := service.DefaultSubRules()
	if err := service.CheckSubRules(def); err != nil {
		t.Fatalf("default rules: %v", err)
	}
	cases := []struct {
		ua, explicit, client string
		rules                []service.SubRule
		want                 Format
	}{
		{ua: "keqdroid/0.25.1", rules: def, want: FormatBase64},
		{ua: "clash-verge/v2.2.3", rules: def, want: FormatMihomo},
		{ua: "SFA/1.14.0", rules: def, want: FormatSingBox},
		{ua: "clash-verge/v2.2.3", explicit: "xray", rules: def, want: FormatXray},
		{ua: "clash-verge/v2.2.3", client: "singbox", rules: def, want: FormatSingBox},
		// A custom rule above the defaults wins; a disabled one is skipped.
		{ua: "clash-verge/v2.2.3", rules: append([]service.SubRule{{Pattern: "(?i)verge", Format: "xray", Enabled: true}}, def...), want: FormatXray},
		{ua: "clash-verge/v2.2.3", rules: append([]service.SubRule{{Pattern: "(?i)verge", Format: "xray"}}, def...), want: FormatMihomo},
		{ua: "clash-verge/v2.2.3", rules: nil, want: FormatBase64},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("User-Agent", c.ua)
		if got := Detect(r, c.explicit, c.client, c.rules); got != c.want {
			t.Errorf("%s (%q, %q): got %s, want %s", c.ua, c.explicit, c.client, got, c.want)
		}
	}
	if err := service.CheckSubRules([]service.SubRule{{Pattern: "(", Format: "base64"}}); err == nil {
		t.Error("a broken regexp passed")
	}
	if err := service.CheckSubRules([]service.SubRule{{Pattern: "x", Format: "yaml"}}); err == nil {
		t.Error("an unknown format passed")
	}
}

func TestProbingBansOnlyRealAddresses(t *testing.T) {
	h := NewHandler(nil, nil)
	for i := 0; i < 50; i++ {
		h.miss("127.0.0.1")
		h.miss("198.51.100.7")
	}
	if h.banned("127.0.0.1") {
		t.Fatal("loopback (unknown client) banned: everyone would get 404")
	}
	if !h.banned("198.51.100.7") {
		t.Fatal("a real prober not banned")
	}
}
