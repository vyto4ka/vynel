package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/store"
	"github.com/vyto4ka/vynel/internal/xrayconf"
)

func customSource(t *testing.T, id, fingerprint string) string {
	t.Helper()
	b := must(xrayconf.GetTemplate("vless-reality-selfsteal"))
	src := strings.Replace(b.Source, "id: vless-reality-selfsteal", "id: "+id, 1)
	src = strings.Replace(src, "fingerprint: ${FINGERPRINT}", "fingerprint: "+fingerprint, 1)
	return strings.Replace(src, "title: VLESS Reality (self-steal)", "title: Моя Reality", 1)
}

func TestCustomTemplates(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second) // a deadlock fails fast
	defer cancel()
	dir := t.TempDir()
	st := must(store.Open(ctx, filepath.Join(dir, "panel.db")))
	defer st.Close()
	s := New(st)
	if err := s.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}

	// Built-in ids are reserved; broken templates are rejected with the reason.
	if _, err := s.SaveTemplate(ctx, ActorCLI, "", must(xrayconf.GetTemplate("vless-reality-selfsteal")).Source); err == nil {
		t.Fatal("saved over a built-in template")
	}
	broken := strings.Replace(customSource(t, "my-reality", "chrome"), "${REALITY_SHORT_ID}\"]", "${NO_SUCH_VAR}\"]", 1)
	if _, err := s.SaveTemplate(ctx, ActorCLI, "", broken); err == nil || !strings.Contains(err.Error(), "NO_SUCH_VAR") {
		t.Fatalf("broken template: %v", err)
	}
	if _, err := s.SaveTemplate(ctx, ActorCLI, "", strings.Replace(customSource(t, "my-reality", "chrome"), "fingerprint: chrome", "fingerprint: ${TYPO}", 1)); err == nil {
		t.Fatal("unknown variable in host section accepted")
	}

	tpl := must(s.SaveTemplate(ctx, ActorCLI, "", customSource(t, "my-reality", "chrome")))
	if !tpl.Custom {
		t.Fatal("not marked custom")
	}
	node, _, err := s.CreateNode(ctx, ActorCLI, NodeInput{Name: "Нидерланды", Country: "nl", Domain: "nl.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	prof := must(s.CreateProfile(ctx, ActorCLI, ProfileInput{Name: "Моя", TemplateID: "my-reality"}))
	ni := must(s.AttachProfile(ctx, ActorCLI, AttachInput{NodeID: node.ID, ProfileID: prof.ID}))
	if h := must(s.HostFor(ctx, ni)); h.Fingerprint != "chrome" {
		t.Fatalf("host fingerprint %q", h.Fingerprint)
	}

	// Editing the template changes every inbound; a change that breaks one is refused.
	must(s.SaveTemplate(ctx, ActorCLI, "my-reality", customSource(t, "my-reality", "safari")))
	if h := must(s.HostFor(ctx, ni)); h.Fingerprint != "safari" {
		t.Fatalf("after edit: %q", h.Fingerprint)
	}
	noPort := strings.Replace(customSource(t, "my-reality", "safari"), "    default: 443\n", "", 1)
	if _, err := s.SaveTemplate(ctx, ActorCLI, "my-reality", noPort); err == nil {
		t.Fatal("a template without the PORT default broke nothing?")
	}
	if _, err := s.SaveTemplate(ctx, ActorCLI, "my-reality", customSource(t, "renamed", "safari")); err == nil {
		t.Fatal("renamed a template in use")
	}
	if err := s.DeleteTemplate(ctx, ActorCLI, "my-reality"); err == nil {
		t.Fatal("deleted a template in use")
	}

	// Another process (the CLI) sees the template.
	s2 := New(st)
	ds, err := s2.DesiredStates(ctx)
	if err != nil || len(ds[node.ID].Inbounds) != 1 {
		t.Fatalf("second service: %v", err)
	}
	list := must(s2.ProfileTemplates(ctx))
	if last := list[len(list)-1]; last.ID != "my-reality" || len(last.Profiles) != 1 {
		t.Fatalf("template list: %+v", last)
	}

	// Previews do not save.
	_, pv, err := s.PreviewProfile(ctx, prof.ID, ProfileInput{Override: map[string]any{"sniffing": map[string]any{"enabled": false}}})
	if err != nil || len(pv) != 1 || pv[0].Err != nil {
		t.Fatalf("preview: %v %+v", err, pv)
	}
	if sn := pv[0].Rendered.Inbound["sniffing"].(map[string]any); sn["enabled"] != false {
		t.Fatalf("preview not applied: %v", sn)
	}
	if p := must(s.ProfileByName(ctx, "Моя")); len(p.Override) != 0 {
		t.Fatal("preview was saved")
	}
	port := 8443
	_, r, err := s.PreviewNodeInbound(ctx, ni.ID, NodeInboundInput{PortOverride: &port})
	if err != nil || r.Inbound["port"] != 8443 {
		t.Fatalf("inbound preview: %v %v", err, r)
	}
	if again := must(s.RenderNodeInbound(ctx, ni.ID)); again.Inbound["port"] != 443 {
		t.Fatal("inbound preview was saved")
	}

	// Regenerating profile keys and a bad tag pattern.
	if _, err := s.UpdateProfile(ctx, ActorCLI, prof.ID, ProfileInput{TagPattern: "FIXED"}); err == nil {
		t.Fatal("tag pattern without the node code accepted")
	}

	if err := s.DetachInbound(ctx, ActorCLI, ni.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteProfile(ctx, ActorCLI, prof.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTemplate(ctx, ActorCLI, "my-reality"); err != nil {
		t.Fatal(err)
	}
}
