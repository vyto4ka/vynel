package subscription

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
	"golang.org/x/net/html"
)

// An external protocol without a handler must not navigate the preview iframe.
// Public subscription links retain their existing launch behavior.
func TestPreviewLinkTargets(t *testing.T) {
	e := newEnv(t)
	d, err := BuildPage(e.user, "https://sub.example.com/s/demo", service.SubBasics{
		Title: "VPN", SupportURL: "https://support.example.com", Announce: "News", AnnounceURL: "https://news.example.com",
	}, service.DefaultSubPage(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, preview := range []bool{false, true} {
		d.Preview = preview
		var buf bytes.Buffer
		if err := RenderPage(&buf, d); err != nil {
			t.Fatal(err)
		}
		doc, err := html.Parse(&buf)
		if err != nil {
			t.Fatal(err)
		}
		found := map[string]bool{}
		var walk func(*html.Node)
		walk = func(n *html.Node) {
			if n.Type == html.ElementNode && n.Data == "a" {
				attrs := map[string]string{}
				for _, a := range n.Attr {
					attrs[a.Key] = a.Val
				}
				href := attrs["href"]
				if strings.HasPrefix(href, "happ://") || href == "https://support.example.com" || href == "https://news.example.com" {
					found[href] = true
					want := ""
					if preview {
						want = "_blank"
					}
					if attrs["target"] != want {
						t.Errorf("preview=%v, %s target=%q", preview, href, attrs["target"])
					}
					if preview && !strings.Contains(attrs["rel"], "noopener") {
						t.Errorf("missing noopener: %s", href)
					}
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
		}
		walk(doc)
		if len(found) != 3 {
			t.Fatalf("missing links: %v", found)
		}
	}
}

func TestPageExtrasAndDeviceDelete(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	now := time.Now()
	prefix := e.svc.SubPrefix(ctx)

	// Register a HWID device by hitting the subscription endpoint as an app.
	req := httptest.NewRequest(http.MethodGet, prefix+e.user.SubToken, nil)
	req.Header.Set("User-Agent", "Happ/4.1.0")
	req.Header.Set("x-hwid", "device-hwid-001")
	req.Header.Set("x-device-model", "iPhone 16 Pro")
	req.Header.Set("x-device-os", "iOS")
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("sub get: %d %s", w.Code, w.Body.String())
	}

	devs, err := e.svc.Devices(ctx, e.user.ID)
	if err != nil || len(devs) != 1 {
		t.Fatalf("expected 1 device, got %d (err=%v)", len(devs), err)
	}

	// Render page with extras and verify sections exist.
	d, err := BuildPage(e.user, "https://sub.example.com"+prefix+e.user.SubToken, service.SubBasics{Title: "Vynel"}, service.DefaultSubPage(), now)
	if err != nil {
		t.Fatal(err)
	}
	PopulatePageExtras(&d, devs, 5, false, []service.DayTraffic{
		{Day: now.AddDate(0, 0, -1).Unix(), Down: 2 << 30, Up: 512 << 20},
		{Day: now.Unix(), Down: 4 << 30, Up: 1 << 30},
	}, prefix+e.user.SubToken+"/devices/", now)
	var buf bytes.Buffer
	if err := RenderPage(&buf, d); err != nil {
		t.Fatal(err)
	}
	htmlStr := buf.String()
	for _, want := range []string{"Трафик по дням", "Устройства (HWID)", "iPhone 16 Pro", "os-tabs", "dev-del"} {
		if !strings.Contains(htmlStr, want) {
			t.Fatalf("rendered page missing %q", want)
		}
	}

	// Cross-site POST without X-Vynel must be refused and keep the device intact.
	noCSRFReq := httptest.NewRequest(http.MethodPost, fmt.Sprintf("%s%s/devices/%d/delete", prefix, e.user.SubToken, devs[0].ID), nil)
	noCSRFW := httptest.NewRecorder()
	e.h.ServeHTTP(noCSRFW, noCSRFReq)
	if noCSRFW.Code != http.StatusForbidden {
		t.Fatalf("device delete without X-Vynel: got %d, want 403", noCSRFW.Code)
	}
	if still, _ := e.svc.Devices(ctx, e.user.ID); len(still) != 1 {
		t.Fatalf("device removed without X-Vynel: %d", len(still))
	}

	// Delete the device via POST {prefix}{token}/devices/{id}/delete with X-Vynel: 1.
	delReq := httptest.NewRequest(http.MethodPost, fmt.Sprintf("%s%s/devices/%d/delete", prefix, e.user.SubToken, devs[0].ID), nil)
	delReq.Header.Set("X-Vynel", "1")
	delW := httptest.NewRecorder()
	e.h.ServeHTTP(delW, delReq)
	if delW.Code != http.StatusOK || !strings.Contains(delW.Body.String(), `"ok":true`) {
		t.Fatalf("device delete: %d %s", delW.Code, delW.Body.String())
	}

	// Verify device is removed.
	after, err := e.svc.Devices(ctx, e.user.ID)
	if err != nil || len(after) != 0 {
		t.Fatalf("expected 0 devices after delete, got %d (err=%v)", len(after), err)
	}

	// Random POST path must return 404 (stealth) even without X-Vynel.
	badReq := httptest.NewRequest(http.MethodPost, prefix+e.user.SubToken+"/unknown", nil)
	badW := httptest.NewRecorder()
	e.h.ServeHTTP(badW, badReq)
	if badW.Code != http.StatusNotFound {
		t.Fatalf("expected 404 on bad POST, got %d", badW.Code)
	}
	_ = store.Device{}
}

func TestPageHostInjectionAndSafeURL(t *testing.T) {
	e := newEnv(t)
	if err := e.svc.SetSetting(e.ctx, actor, service.SettingSubDomain, ""); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetSetting(e.ctx, actor, service.SettingWebDomain, "panel.example.com"); err != nil {
		t.Fatal(err)
	}
	prefix := e.svc.SubPrefix(e.ctx)

	// Spoofed Host header when sub.domain is unset must never appear in rendered links or QR.
	req := httptest.NewRequest(http.MethodGet, "https://evil.attacker.test"+prefix+e.user.SubToken, nil)
	req.Host = "evil.attacker.test"
	req.Header.Set("Accept", "text/html")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "evil.attacker.test") {
		t.Fatalf("untrusted Host leaked into subscription page HTML")
	}
	if w.Header().Get("Referrer-Policy") != "no-referrer" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("missing security headers: %v", w.Header())
	}

	// Trusted web.domain is accepted as fallback.
	okReq := httptest.NewRequest(http.MethodGet, "https://panel.example.com"+prefix+e.user.SubToken, nil)
	okReq.Host = "panel.example.com"
	okReq.Header.Set("Accept", "text/html")
	okReq.Header.Set("User-Agent", "Mozilla/5.0")
	okW := httptest.NewRecorder()
	e.h.ServeHTTP(okW, okReq)
	if okW.Code != http.StatusOK || !strings.Contains(okW.Body.String(), "https://panel.example.com"+prefix+e.user.SubToken) {
		t.Fatalf("trusted web.domain fallback failed: %d", okW.Code)
	}

	// WHATWG control-character scheme bypasses are rejected by safeURL.
	for _, bad := range []string{"java\tscript:alert(1)", "java\nscript:alert(1)", "\x01javascript:alert(1)", "DATA:text/html,<script>"} {
		if got := safeURL(bad); got != "" {
			t.Fatalf("safeURL(%q) = %q, want empty", bad, got)
		}
	}
}

func TestPreviewPageLangAndNoDuplicateHeader(t *testing.T) {
	e := newEnv(t)
	d, err := BuildPage(e.user, "https://sub.example.com/s/demo", service.SubBasics{Title: "VPN"}, service.DefaultSubPage(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	d.Preview = true
	ApplyPageLang(&d, "en")
	var buf bytes.Buffer
	if err := RenderPage(&buf, d); err != nil {
		t.Fatal(err)
	}
	htmlStr := buf.String()
	if strings.Contains(htmlStr, "Предпросмотр") || strings.Contains(htmlStr, "Открыть сохранённую страницу") {
		t.Fatalf("duplicate Russian preview header must not be rendered inside iframe")
	}
	for _, want := range []string{`<html lang="en" translate="no"`, "Traffic", "Valid until", "Connect"} {
		if !strings.Contains(htmlStr, want) {
			t.Fatalf("English preview page missing %q", want)
		}
	}
}

func TestPageExternalScriptOSDetectionAndDesktopLayout(t *testing.T) {
	e := newEnv(t)
	prefix := e.svc.SubPrefix(e.ctx)

	// Open the page from a Windows browser (User-Agent + Client Hint).
	req := httptest.NewRequest(http.MethodGet, prefix+e.user.SubToken, nil)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("Sec-CH-UA-Platform", `"Windows"`)
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("page status %d", w.Code)
	}
	htmlStr := w.Body.String()

	// Must have no inline executable scripts or inline onclick attributes (blocked by proxy CSP).
	if strings.Contains(htmlStr, "onclick=") {
		t.Fatalf("inline onclick attribute present in page HTML")
	}
	scriptURL := prefix + e.user.SubToken + "/page.js"
	for _, want := range []string{
		`src="` + scriptURL + `"`,
		`id="sub-cfg" type="application/json"`,
		`Ваша ОС: Windows`,
		`class="os-tab on detected" data-os="windows"`,
		`Как подключиться на Windows`,
		`@media (min-width:900px)`,
		`max-width:1080px`,
	} {
		if !strings.Contains(htmlStr, want) {
			t.Fatalf("Windows subscription page missing %q", want)
		}
	}

	// Fetch the external JS asset via the subscription handler.
	jsReq := httptest.NewRequest(http.MethodGet, scriptURL, nil)
	jsW := httptest.NewRecorder()
	e.h.ServeHTTP(jsW, jsReq)
	if jsW.Code != http.StatusOK || !strings.Contains(jsW.Header().Get("Content-Type"), "javascript") {
		t.Fatalf("page.js status=%d ct=%q", jsW.Code, jsW.Header().Get("Content-Type"))
	}
	if !strings.Contains(jsW.Body.String(), "copyValue") || !strings.Contains(jsW.Body.String(), "selectOS") {
		t.Fatalf("unexpected page.js body")
	}

	// Unknown token must still return 404 for page.js (stealth).
	badJSReq := httptest.NewRequest(http.MethodGet, prefix+"nonexistent-token-xyz/page.js", nil)
	badJSW := httptest.NewRecorder()
	e.h.ServeHTTP(badJSW, badJSReq)
	if badJSW.Code != http.StatusNotFound {
		t.Fatalf("unknown token page.js got %d, want 404", badJSW.Code)
	}
}
