package xrayconf

import (
	"testing"
)

func TestBuildConfigWithWarpOrdering(t *testing.T) {
	base, err := ParseBase(DefaultBaseJSON)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := BuildConfig(NodeConfig{
		Base:    base,
		APIAddr: "127.0.0.1:10085",
		Inbounds: []*RenderedInbound{
			{
				Tag:      "vless-1",
				Protocol: "vless",
				EgressIP: "203.0.113.10",
				Inbound: map[string]any{
					"tag":      "vless-1",
					"listen":   "0.0.0.0",
					"port":     443,
					"protocol": "vless",
					"settings": map[string]any{"decryption": "none"},
				},
			},
		},
		FrontRules: []any{
			map[string]any{"user": []any{"1.c1"}, "outboundTag": "CASCADE_1"},
		},
		Warp: &WarpConfig{
			SecretKey:     "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
			Addresses:     []string{"172.16.0.2/32", "2606:4700:110::1/128"},
			PeerPublicKey: "bmXOC+F1FxEMF9dyiK2H5/1SUtzH0JuVo51h2wPfgyo=",
			Reserved:      []int{10, 20, 30},
			Endpoint:      "engage.cloudflareclient.com:2408",
			MTU:           1280,
			Keepalive:     25,
			Domains:       []string{"geosite:openai"},
			IPs:           []string{"geoip:netflix"},
		},
	})
	if err != nil {
		t.Fatalf("BuildConfig: %v", err)
	}

	// Verify VYNEL_WARP outbound has noKernelTun=true.
	obs := cfg["outbounds"].([]any)
	var warpOut map[string]any
	for _, o := range obs {
		m := o.(map[string]any)
		if m["tag"] == WarpTag {
			warpOut = m
		}
	}
	if warpOut == nil {
		t.Fatalf("missing %s outbound", WarpTag)
	}
	settings := warpOut["settings"].(map[string]any)
	if settings["noKernelTun"] != true {
		t.Fatalf("noKernelTun must be true, got %v", settings["noKernelTun"])
	}
	if warpOut["targetStrategy"] != "ForceIPv4v6" {
		t.Fatalf("targetStrategy must be ForceIPv4v6, got %v", warpOut["targetStrategy"])
	}
	sockopt := warpOut["streamSettings"].(map[string]any)["sockopt"].(map[string]any)
	if sockopt["domainStrategy"] != "ForceIPv4v6" {
		t.Fatalf("sockopt.domainStrategy must be ForceIPv4v6, got %v", sockopt["domainStrategy"])
	}

	// Verify routing.domainStrategy auto-set to IPIfNonMatch.
	routing := cfg["routing"].(map[string]any)
	if routing["domainStrategy"] != "IPIfNonMatch" {
		t.Fatalf("expected IPIfNonMatch, got %v", routing["domainStrategy"])
	}

	// Verify exact rule order: CASCADE -> BLOCK(x3) -> WARP_DOM -> WARP_IP -> DIRECT@ip -> DIRECT.
	rules := routing["rules"].([]any)
	wantTags := []string{"CASCADE_1", "BLOCK", "BLOCK", "BLOCK", WarpTag, WarpTag, "DIRECT@203.0.113.10", "DIRECT"}
	if len(rules) != len(wantTags) {
		t.Fatalf("expected %d rules, got %d: %#v", len(wantTags), len(rules), rules)
	}
	for i, want := range wantTags {
		got := rules[i].(map[string]any)["outboundTag"]
		if got != want {
			t.Fatalf("rule %d: want outboundTag %q, got %q", i, want, got)
		}
	}
}
