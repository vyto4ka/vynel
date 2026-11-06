package xrayconf

import (
	"strings"
	"testing"
)

// The preview the template editor shows: a built-in template renders without an error and
// its ${VARIABLES} are substituted (input variables get sample values, generate variables are
// generated once in memory, secrets are marked by the caller).
func TestPreviewRenderBuiltin(t *testing.T) {
	b, err := GetTemplate("vless-reality-selfsteal")
	if err != nil {
		t.Fatal(err)
	}
	_, r, host, unresolved, err := PreviewRender([]byte(b.Source))
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(unresolved) != 0 {
		t.Fatalf("unresolved variables: %v", unresolved)
	}
	in, err := MarshalIndent(r.Inbound)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(in), "${") {
		t.Fatalf("placeholders left in the rendered inbound:\n%s", in)
	}
	if r.Tag != "VLESS_NL" {
		t.Fatalf("tag: %q", r.Tag)
	}
	if pk, _ := r.Values["REALITY_PRIVATE_KEY"].(string); pk == "" {
		t.Fatal("REALITY_PRIVATE_KEY was not generated")
	}
	if sn, _ := r.Inbound["streamSettings"].(map[string]any)["realitySettings"].(map[string]any)["serverNames"].([]any); len(sn) == 0 {
		t.Fatalf("serverNames: %v", sn)
	}
	if host == nil {
		t.Fatal("no host preview")
	}
	hb, _ := MarshalIndent(host)
	if strings.Contains(string(hb), "${") {
		t.Fatalf("placeholders left in the host preview:\n%s", hb)
	}
	if a, _ := host["address"].(string); a != "nl.example.com" {
		t.Fatalf("host address: %q", a)
	}
}

// A half-written template still gets a preview; the placeholders that cannot be resolved are
// reported by name instead of failing the render.
func TestPreviewRenderUnresolved(t *testing.T) {
	src := `
id: preview-draft
version: 1
title: Draft
tag_pattern: DRAFT_${NODE_CODE}
protocol: vless
client: { flow: "" }
variables:
  - name: DOMAIN
    scope: node
    source: input
    validate: domain
xray_inbound: |
  {
    "tag": "${TAG}",
    "port": 443,
    "listen": "127.0.0.1",
    "protocol": "vless",
    "settings": { "clients": [], "decryption": "none" },
    "streamSettings": { "network": "xhttp", "xhttpSettings": { "path": "${UNDECLARED}" } }
  }
host:
  address: ${DOMAIN}
  port: ${INBOUND_PORT}
`
	_, r, host, unresolved, err := PreviewRender([]byte(src))
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(unresolved) != 1 || unresolved[0] != "UNDECLARED" {
		t.Fatalf("unresolved: %v", unresolved)
	}
	if r.Tag != "DRAFT_NL" {
		t.Fatalf("tag: %q", r.Tag)
	}
	if a, _ := host["address"].(string); a != "sample.example.com" {
		t.Fatalf("host address: %q", a)
	}
}

// A YAML error is reported, not turned into a preview.
func TestPreviewRenderBadYAML(t *testing.T) {
	if _, _, _, _, err := PreviewRender([]byte("id: x\nxray_inbound: '{'")); err == nil {
		t.Fatal("no error for broken YAML")
	}
}
